/*
Copyright 2025 The Hyperlight Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

const (
	resourceName       = "hyperlight.dev/hypervisor"
	serverSock         = pluginapi.DevicePluginPath + "hyperlight.sock"
	kubeletSock        = pluginapi.KubeletSocket
	cdiSpecPath        = "/var/run/cdi/hyperlight.json"
	defaultDeviceCount = 2000  // Conservative default for MSHV; KVM can handle more
	defaultDeviceUID   = 65534 // Default UID for device node in container (nobody)
	defaultDeviceGID   = 65534 // Default GID for device node in container (nobody)
)

type HyperlightDevicePlugin struct {
	devices    []*pluginapi.Device
	server     *grpc.Server
	devicePath string
	hypervisor string
	stopCh     chan struct{}
	pluginapi.UnimplementedDevicePluginServer
}

func NewHyperlightDevicePlugin() (*HyperlightDevicePlugin, error) {
	var devicePath, hypervisor string

	// Auto-detect hypervisor - prefer MSHV over KVM
	if _, err := os.Stat("/dev/mshv"); err == nil {
		devicePath = "/dev/mshv"
		hypervisor = "mshv"
	} else if _, err := os.Stat("/dev/kvm"); err == nil {
		devicePath = "/dev/kvm"
		hypervisor = "kvm"
	} else {
		return nil, fmt.Errorf("no supported hypervisor found (/dev/kvm or /dev/mshv)")
	}

	klog.Infof("Detected hypervisor: %s at %s", hypervisor, devicePath)

	// Create CDI spec
	if err := writeCDISpec(hypervisor, devicePath); err != nil {
		return nil, fmt.Errorf("failed to write CDI spec: %v", err)
	}

	// Get device count from environment, default to 2000
	// This represents concurrent allocations, not physical devices.
	// The hypervisor device (/dev/kvm or /dev/mshv) is shared - each
	// allocation just grants access to the same underlying device.
	// KVM: effectively unlimited concurrent VMs
	// MSHV: ~2000 concurrent VMs recommended
	numDevices := defaultDeviceCount
	if countStr := os.Getenv("DEVICE_COUNT"); countStr != "" {
		if count, err := strconv.Atoi(countStr); err == nil && count > 0 {
			numDevices = count
		} else {
			klog.Warningf("Invalid DEVICE_COUNT '%s', using default %d", countStr, defaultDeviceCount)
		}
	}

	devices := make([]*pluginapi.Device, numDevices)
	for i := 0; i < numDevices; i++ {
		devices[i] = &pluginapi.Device{
			ID:     fmt.Sprintf("%s-%d", hypervisor, i),
			Health: pluginapi.Healthy,
		}
	}
	klog.Infof("Advertising %d hypervisor devices (configurable via DEVICE_COUNT)", numDevices)

	return &HyperlightDevicePlugin{
		devices:    devices,
		devicePath: devicePath,
		hypervisor: hypervisor,
		stopCh:     make(chan struct{}),
	}, nil
}

func writeCDISpec(hypervisor, devicePath string) error {
	// Get UID/GID from environment, default to 65534 (nobody)
	// These control the ownership of the device node inside containers
	uid := defaultDeviceUID
	if uidStr := os.Getenv("DEVICE_UID"); uidStr != "" {
		if parsed, err := strconv.Atoi(uidStr); err == nil && parsed >= 0 {
			uid = parsed
		} else {
			klog.Warningf("Invalid DEVICE_UID '%s', using default %d", uidStr, defaultDeviceUID)
		}
	}

	gid := defaultDeviceGID
	if gidStr := os.Getenv("DEVICE_GID"); gidStr != "" {
		if parsed, err := strconv.Atoi(gidStr); err == nil && parsed >= 0 {
			gid = parsed
		} else {
			klog.Warningf("Invalid DEVICE_GID '%s', using default %d", gidStr, defaultDeviceGID)
		}
	}

	klog.Infof("CDI device ownership: uid=%d, gid=%d (configurable via DEVICE_UID/DEVICE_GID)", uid, gid)

	spec := fmt.Sprintf(`{
  "cdiVersion": "0.6.0",
  "kind": "hyperlight.dev/hypervisor",
  "devices": [
    {
      "name": "%s",
      "containerEdits": {
        "deviceNodes": [
          {
            "path": "%s",
            "type": "c",
            "permissions": "rw",
            "uid": %d,
            "gid": %d
          }
        ],
        "env": [
          "HYPERLIGHT_HYPERVISOR=%s",
          "HYPERLIGHT_DEVICE_PATH=%s"
        ]
      }
    }
  ]
}`, hypervisor, devicePath, uid, gid, hypervisor, devicePath)

	if err := os.MkdirAll(filepath.Dir(cdiSpecPath), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(cdiSpecPath, []byte(spec), 0644); err != nil {
		return err
	}
	klog.Infof("CDI spec written to %s", cdiSpecPath)
	return nil
}

// GetDevicePluginOptions returns options for the device plugin
func (p *HyperlightDevicePlugin) GetDevicePluginOptions(ctx context.Context, req *pluginapi.Empty) (*pluginapi.DevicePluginOptions, error) {
	return &pluginapi.DevicePluginOptions{
		PreStartRequired:                false,
		GetPreferredAllocationAvailable: false,
	}, nil
}

// ListAndWatch lists devices and watches for changes
func (p *HyperlightDevicePlugin) ListAndWatch(req *pluginapi.Empty, srv pluginapi.DevicePlugin_ListAndWatchServer) error {
	klog.Infof("ListAndWatch called, sending %d devices", len(p.devices))

	if err := srv.Send(&pluginapi.ListAndWatchResponse{Devices: p.devices}); err != nil {
		return err
	}

	// Health check loop
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return nil
		case <-ticker.C:
			newHealth := pluginapi.Healthy
			if _, err := os.Stat(p.devicePath); err != nil {
				newHealth = pluginapi.Unhealthy
				klog.Warningf("Device %s not found, marking all devices unhealthy", p.devicePath)
			}

			// Check if health changed (compare against first device as representative)
			if p.devices[0].Health != newHealth {
				// Update ALL devices - they all share the same underlying hypervisor device
				for i := range p.devices {
					p.devices[i].Health = newHealth
				}
				klog.Infof("Device health changed to %s for all %d devices", newHealth, len(p.devices))
				if err := srv.Send(&pluginapi.ListAndWatchResponse{Devices: p.devices}); err != nil {
					return err
				}
			}
		}
	}
}

// Allocate allocates devices to a container
func (p *HyperlightDevicePlugin) Allocate(ctx context.Context, req *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	klog.V(2).Infof("Allocate called for %d containers", len(req.ContainerRequests))

	responses := make([]*pluginapi.ContainerAllocateResponse, len(req.ContainerRequests))

	for i := range req.ContainerRequests {
		responses[i] = &pluginapi.ContainerAllocateResponse{
			// Use CDI device injection
			CdiDevices: []*pluginapi.CDIDevice{
				{
					Name: fmt.Sprintf("hyperlight.dev/hypervisor=%s", p.hypervisor),
				},
			},
		}
		klog.V(2).Infof("Allocated CDI device: hyperlight.dev/hypervisor=%s", p.hypervisor)
	}

	return &pluginapi.AllocateResponse{ContainerResponses: responses}, nil
}

// PreStartContainer is called before container start (not used)
func (p *HyperlightDevicePlugin) PreStartContainer(ctx context.Context, req *pluginapi.PreStartContainerRequest) (*pluginapi.PreStartContainerResponse, error) {
	return &pluginapi.PreStartContainerResponse{}, nil
}

// GetPreferredAllocation returns preferred allocation (not used)
func (p *HyperlightDevicePlugin) GetPreferredAllocation(ctx context.Context, req *pluginapi.PreferredAllocationRequest) (*pluginapi.PreferredAllocationResponse, error) {
	return &pluginapi.PreferredAllocationResponse{}, nil
}

func (p *HyperlightDevicePlugin) Start() error {
	// Reset stop channel for restart scenarios
	p.stopCh = make(chan struct{})

	// Remove old socket
	if err := os.Remove(serverSock); err != nil && !os.IsNotExist(err) {
		klog.Warningf("Failed to remove old socket: %v", err)
	}

	listener, err := net.Listen("unix", serverSock)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %v", serverSock, err)
	}

	p.server = grpc.NewServer()
	pluginapi.RegisterDevicePluginServer(p.server, p)

	go func() {
		klog.Infof("Starting gRPC server on %s", serverSock)
		if err := p.server.Serve(listener); err != nil {
			klog.V(1).Infof("gRPC server stopped: %v", err)
		}
	}()

	// Wait for server to start
	time.Sleep(time.Second)

	// Register with kubelet
	return p.Register()
}

func (p *HyperlightDevicePlugin) Register() error {
	conn, err := grpc.Dial(kubeletSock,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, "unix", addr)
		}),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to kubelet: %v", err)
	}
	defer conn.Close()

	client := pluginapi.NewRegistrationClient(conn)

	req := &pluginapi.RegisterRequest{
		Version:      pluginapi.Version,
		Endpoint:     filepath.Base(serverSock),
		ResourceName: resourceName,
		Options: &pluginapi.DevicePluginOptions{
			PreStartRequired:                false,
			GetPreferredAllocationAvailable: false,
		},
	}

	_, err = client.Register(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to register with kubelet: %v", err)
	}

	klog.Infof("Registered with kubelet as %s", resourceName)
	return nil
}

func (p *HyperlightDevicePlugin) Stop() {
	close(p.stopCh)
	if p.server != nil {
		p.server.Stop()
	}
	os.Remove(serverSock)
	klog.Info("Device plugin stopped")
}

// newFSWatcher creates a filesystem watcher for kubelet restart detection.
// This is the industry-standard approach used by NVIDIA, Intel, and other device plugins.
func newFSWatcher(files ...string) (*fsnotify.Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	for _, f := range files {
		if err := watcher.Add(f); err != nil {
			watcher.Close()
			return nil, err
		}
	}

	return watcher, nil
}

// watchKubeletRestart monitors for kubelet restarts using fsnotify.
// When kubelet restarts, it deletes all sockets in /var/lib/kubelet/device-plugins/.
// This function blocks until it detects a relevant filesystem event.
func (p *HyperlightDevicePlugin) watchKubeletRestart() {
	klog.Info("Watching for kubelet restart using fsnotify...")

	watcher, err := newFSWatcher(pluginapi.DevicePluginPath)
	if err != nil {
		klog.Errorf("Failed to create fsnotify watcher, falling back to polling: %v", err)
		p.watchKubeletRestartPolling()
		return
	}
	defer watcher.Close()

	for {
		select {
		case <-p.stopCh:
			return
		case event := <-watcher.Events:
			if event.Name == serverSock && (event.Op&fsnotify.Remove) == fsnotify.Remove {
				klog.Info("Plugin socket deleted - kubelet may have restarted")
				return
			}
			// Also watch for kubelet socket recreation (indicates kubelet restart complete)
			if event.Name == kubeletSock && (event.Op&fsnotify.Create) == fsnotify.Create {
				klog.Info("Kubelet socket recreated - kubelet restart detected")
				return
			}
		case err := <-watcher.Errors:
			klog.Warningf("fsnotify error: %v", err)
		}
	}
}

// watchKubeletRestartPolling is a fallback method using polling.
// Used when fsnotify is unavailable.
func (p *HyperlightDevicePlugin) watchKubeletRestartPolling() {
	klog.Info("Watching for kubelet restart (polling)...")

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			if _, err := os.Stat(serverSock); os.IsNotExist(err) {
				klog.Info("Plugin socket deleted - kubelet may have restarted")
				return
			}
		}
	}
}

func main() {
	klog.InitFlags(nil)
	flag.Parse()
	defer klog.Flush()

	klog.Info("Starting Hyperlight Device Plugin")

	plugin, err := NewHyperlightDevicePlugin()
	if err != nil {
		klog.Fatalf("Failed to create device plugin: %v", err)
	}

	// Handle signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start plugin with restart handling
	go func() {
		for {
			if err := plugin.Start(); err != nil {
				klog.Errorf("Failed to start device plugin: %v", err)
				time.Sleep(5 * time.Second)
				continue
			}

			// Watch for kubelet restart (socket deletion)
			// When kubelet restarts, it deletes all sockets in /var/lib/kubelet/device-plugins/
			plugin.watchKubeletRestart()

			// If we get here, kubelet restarted - stop current server and re-register
			klog.Info("Detected kubelet restart, re-registering...")
			plugin.server.Stop()
			time.Sleep(time.Second) // Brief pause before restart
		}
	}()

	sig := <-sigCh
	klog.Infof("Received signal %v, shutting down", sig)
	plugin.Stop()
}

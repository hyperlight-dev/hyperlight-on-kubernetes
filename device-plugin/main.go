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
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

const (
	resourceName       = "hyperlight.dev/hypervisor"
	serverSock         = pluginapi.DevicePluginPath + "hyperlight.sock"
	kubeletSock        = pluginapi.KubeletSocket
	cdiSpecPath        = "/var/run/cdi/hyperlight.json"
	defaultDeviceCount = 2000 // Conservative default for MSHV; KVM can handle more
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

	log.Printf("Detected hypervisor: %s at %s", hypervisor, devicePath)

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
			log.Printf("Invalid DEVICE_COUNT '%s', using default %d", countStr, defaultDeviceCount)
		}
	}

	devices := make([]*pluginapi.Device, numDevices)
	for i := 0; i < numDevices; i++ {
		devices[i] = &pluginapi.Device{
			ID:     fmt.Sprintf("%s-%d", hypervisor, i),
			Health: pluginapi.Healthy,
		}
	}
	log.Printf("Advertising %d hypervisor devices (configurable via DEVICE_COUNT)", numDevices)

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
			log.Printf("Invalid DEVICE_UID '%s', using default %d", uidStr, defaultDeviceUID)
		}
	}

	gid := defaultDeviceGID
	if gidStr := os.Getenv("DEVICE_GID"); gidStr != "" {
		if parsed, err := strconv.Atoi(gidStr); err == nil && parsed >= 0 {
			gid = parsed
		} else {
			log.Printf("Invalid DEVICE_GID '%s', using default %d", gidStr, defaultDeviceGID)
		}
	}

	log.Printf("CDI device ownership: uid=%d, gid=%d (configurable via DEVICE_UID/DEVICE_GID)", uid, gid)

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
	log.Printf("CDI spec written to %s", cdiSpecPath)
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
	log.Printf("ListAndWatch called, sending %d devices", len(p.devices))

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
			health := pluginapi.Healthy
			if _, err := os.Stat(p.devicePath); err != nil {
				health = pluginapi.Unhealthy
				log.Printf("Device %s not found, marking unhealthy", p.devicePath)
			}

			if p.devices[0].Health != health {
				p.devices[0].Health = health
				log.Printf("Device health changed to %s", health)
				if err := srv.Send(&pluginapi.ListAndWatchResponse{Devices: p.devices}); err != nil {
					return err
				}
			}
		}
	}
}

// Allocate allocates devices to a container
func (p *HyperlightDevicePlugin) Allocate(ctx context.Context, req *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	log.Printf("Allocate called for %d containers", len(req.ContainerRequests))

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
		log.Printf("Allocated CDI device: hyperlight.dev/hypervisor=%s", p.hypervisor)
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
	// Remove old socket
	if err := os.Remove(serverSock); err != nil && !os.IsNotExist(err) {
		log.Printf("Warning: failed to remove old socket: %v", err)
	}

	listener, err := net.Listen("unix", serverSock)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %v", serverSock, err)
	}

	p.server = grpc.NewServer()
	pluginapi.RegisterDevicePluginServer(p.server, p)

	go func() {
		log.Printf("Starting gRPC server on %s", serverSock)
		if err := p.server.Serve(listener); err != nil {
			log.Printf("gRPC server stopped: %v", err)
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

	log.Printf("Registered with kubelet as %s", resourceName)
	return nil
}

func (p *HyperlightDevicePlugin) Stop() {
	close(p.stopCh)
	if p.server != nil {
		p.server.Stop()
	}
	os.Remove(serverSock)
	log.Println("Device plugin stopped")
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("Starting Hyperlight Device Plugin")

	plugin, err := NewHyperlightDevicePlugin()
	if err != nil {
		log.Fatalf("Failed to create device plugin: %v", err)
	}

	if err := plugin.Start(); err != nil {
		log.Fatalf("Failed to start device plugin: %v", err)
	}

	// Handle signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigCh
	log.Printf("Received signal %v, shutting down", sig)
	plugin.Stop()
}

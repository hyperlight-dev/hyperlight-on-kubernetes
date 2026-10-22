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
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

const (
	resourceName         = "hyperlight.dev/hypervisor"
	deviceAccessResource = "hyperlight.dev/device-access"
	deviceAccessSock     = pluginapi.DevicePluginPath + "hyperlight-access.sock"
	serverSock           = pluginapi.DevicePluginPath + "hyperlight.sock"
	kubeletSock          = pluginapi.KubeletSocket
	cdiSpecPath          = "/var/run/cdi/hyperlight.json"
	defaultDeviceCount   = 2000  // Conservative default for MSHV; KVM can handle more
	defaultDeviceUID     = 65534 // Default UID for device node in container (nobody)
	defaultDeviceGID     = 65534 // Default GID for device node in container (nobody)
)

type HyperlightDevicePlugin struct {
	devices             []*pluginapi.Device
	server              *grpc.Server
	hypervisor          string
	devicePath          string
	resource            string
	deviceAccess        bool
	stopCh              chan struct{}
	mu                  sync.Mutex
	cdiPath             string
	cdiSpec             []byte
	probe               func(context.Context) error
	healthServer        *health.Server
	registered          bool
	watchers            int
	readinessErr        error
	readinessKnown      bool
	checkGate           chan struct{}
	changed             chan struct{}
	socket              string
	kubeletSocket       string
	interval            time.Duration
	registrationTimeout time.Duration
	pluginapi.UnimplementedDevicePluginServer
}

func NewHyperlightDevicePlugin() (*HyperlightDevicePlugin, error) {
	return newDevicePlugin(false)
}

func newDevicePlugin(deviceAccess bool) (*HyperlightDevicePlugin, error) {
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

	return newDevicePluginWithDevice(hypervisor, devicePath, deviceAccess)
}

func newDevicePluginWithDevice(hypervisor, devicePath string, deviceAccess bool) (*HyperlightDevicePlugin, error) {
	klog.Infof("Detected hypervisor: %s at %s", hypervisor, devicePath)

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

	if deviceAccess {
		numDevices = 1
	}
	devices := make([]*pluginapi.Device, numDevices)
	for i := 0; i < numDevices; i++ {
		devices[i] = &pluginapi.Device{
			ID:     fmt.Sprintf("%s-%d", hypervisor, i),
			Health: pluginapi.Unhealthy,
		}
	}
	klog.Infof("Advertising %d hypervisor devices (configurable via DEVICE_COUNT)", numDevices)

	p := &HyperlightDevicePlugin{
		devices:             devices,
		hypervisor:          hypervisor,
		devicePath:          devicePath,
		resource:            resourceName,
		deviceAccess:        deviceAccess,
		stopCh:              make(chan struct{}),
		cdiPath:             cdiSpecPath,
		socket:              serverSock,
		kubeletSocket:       kubeletSock,
		interval:            30 * time.Second,
		registrationTimeout: 5 * time.Second,
	}
	if deviceAccess {
		p.socket = deviceAccessSock
		p.resource = deviceAccessResource
		p.probe = func(ctx context.Context) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			return checkDeviceNode(devicePath)
		}
		return p, nil
	}
	p.cdiSpec = desiredCDISpec(hypervisor, devicePath)
	hostPath := devicePath
	if dir := os.Getenv("HOST_DEVICE_DIR"); dir != "" {
		hostPath = filepath.Join(dir, filepath.Base(devicePath))
	}
	probe := &deviceProbe{hostPath: hostPath}
	p.probe = func(ctx context.Context) error { return probe.check(ctx, hypervisor, devicePath) }
	return p, nil
}

func desiredCDISpec(hypervisor, devicePath string) []byte {
	// Get UID/GID from environment, default to 65534 (nobody)
	// These control the ownership of the device node inside containers
	uid := uint32(defaultDeviceUID)
	if uidStr := os.Getenv("DEVICE_UID"); uidStr != "" {
		if parsed, err := strconv.ParseUint(uidStr, 10, 32); err == nil {
			uid = uint32(parsed)
		} else {
			klog.Warningf("Invalid DEVICE_UID '%s', using default %d", uidStr, defaultDeviceUID)
		}
	}

	gid := uint32(defaultDeviceGID)
	if gidStr := os.Getenv("DEVICE_GID"); gidStr != "" {
		if parsed, err := strconv.ParseUint(gidStr, 10, 32); err == nil {
			gid = uint32(parsed)
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

	return []byte(spec)
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
	p.mu.Lock()
	p.watchers++
	stop := p.stopCh
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.watchers--
		p.updateReadinessLocked()
	}()
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	if err := p.checkReadiness(srv.Context()); err != nil && srv.Context().Err() != nil {
		return srv.Context().Err()
	}
	previous := ""
	for {
		p.mu.Lock()
		state := p.deviceHealthLocked()
		changed := p.changed
		p.mu.Unlock()
		if state != previous {
			devices := make([]*pluginapi.Device, len(p.devices))
			for i, device := range p.devices {
				devices[i] = &pluginapi.Device{ID: device.ID, Health: state}
			}
			if err := srv.Send(&pluginapi.ListAndWatchResponse{Devices: devices}); err != nil {
				return err
			}
			previous = state
		}
		select {
		case <-stop:
			return nil
		case <-srv.Context().Done():
			return srv.Context().Err()
		case <-changed:
		case <-ticker.C:
			if err := p.checkReadiness(srv.Context()); err != nil && srv.Context().Err() != nil {
				return srv.Context().Err()
			}
		}
	}
}

// Allocate allocates devices to a container
func (p *HyperlightDevicePlugin) Allocate(ctx context.Context, req *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	klog.V(2).Infof("Allocate called for %d containers", len(req.ContainerRequests))
	if err := p.checkReadiness(ctx); err != nil {
		if ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		return nil, status.Errorf(codes.Unavailable, "hypervisor allocation is not ready: %v", err)
	}
	for _, container := range req.ContainerRequests {
		for _, id := range container.DevicesIds {
			found := false
			for _, device := range p.devices {
				if device.ID == id {
					found = true
					break
				}
			}
			if !found {
				return nil, status.Errorf(codes.InvalidArgument, "unknown device %q", id)
			}
		}
	}

	responses := make([]*pluginapi.ContainerAllocateResponse, len(req.ContainerRequests))

	for i := range req.ContainerRequests {
		if p.deviceAccess {
			responses[i] = &pluginapi.ContainerAllocateResponse{
				Devices: []*pluginapi.DeviceSpec{{
					HostPath:      p.devicePath,
					ContainerPath: p.devicePath,
					Permissions:   "rw",
				}},
			}
			continue
		}
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
	p.mu.Lock()
	p.stopCh = make(chan struct{})
	p.registered = false
	p.readinessKnown = false
	p.healthServer = health.NewServer()
	p.updateReadinessLocked()
	p.mu.Unlock()
	if err := os.Remove(p.socket); err != nil && !os.IsNotExist(err) {
		return err
	}
	listener, err := net.Listen("unix", p.socket)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	p.server = grpc.NewServer(grpc.WaitForHandlers(true))
	server := p.server
	pluginapi.RegisterDevicePluginServer(p.server, p)
	healthpb.RegisterHealthServer(p.server, p.healthServer)
	p.healthServer.SetServingStatus("liveness", healthpb.HealthCheckResponse_SERVING)
	go func() {
		if err := server.Serve(listener); err != nil {
			klog.V(1).Infof("gRPC server stopped: %v", err)
		}
	}()
	if err := p.Register(); err != nil {
		p.Stop()
		return err
	}
	return nil
}

func (p *HyperlightDevicePlugin) registrationResource() string {
	if p.resource != "" {
		return p.resource
	}
	return resourceName
}

func (p *HyperlightDevicePlugin) Register() error {
	ctx, cancel := context.WithTimeout(context.Background(), p.registrationTimeout)
	defer cancel()
	conn, err := dialPlugin(ctx, p.kubeletSocket)
	if err != nil {
		return fmt.Errorf("connect to kubelet: %w", err)
	}
	defer conn.Close()
	_, err = pluginapi.NewRegistrationClient(conn).Register(ctx, &pluginapi.RegisterRequest{
		Version:      pluginapi.Version,
		Endpoint:     filepath.Base(p.socket),
		ResourceName: p.registrationResource(),
		Options:      &pluginapi.DevicePluginOptions{},
	})
	if err != nil {
		return fmt.Errorf("register with kubelet: %w", err)
	}
	p.mu.Lock()
	p.registered = true
	p.updateReadinessLocked()
	p.mu.Unlock()
	klog.Infof("Registered with kubelet as %s", p.registrationResource())
	return nil
}

func dialPlugin(ctx context.Context, socket string) (*grpc.ClientConn, error) {
	return grpc.DialContext(ctx, "unix://"+socket,
		grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
}

func (p *HyperlightDevicePlugin) Stop() {
	p.mu.Lock()
	p.registered = false
	if p.healthServer != nil {
		p.healthServer.Shutdown()
	}
	select {
	case <-p.stopCh:
	default:
		close(p.stopCh)
	}
	p.mu.Unlock()
	if p.server != nil {
		p.server.Stop()
	}
	os.Remove(p.socket)
	klog.Info("Device plugin stopped")
}

func (p *HyperlightDevicePlugin) updateReadinessLocked() {
	if p.healthServer == nil {
		return
	}
	state := healthpb.HealthCheckResponse_NOT_SERVING
	if p.registered && p.watchers > 0 && p.deviceHealthLocked() == pluginapi.Healthy {
		state = healthpb.HealthCheckResponse_SERVING
	}
	p.healthServer.SetServingStatus("", state)
}

func (p *HyperlightDevicePlugin) deviceHealthLocked() string {
	if p.readinessKnown && p.readinessErr == nil {
		return pluginapi.Healthy
	}
	return pluginapi.Unhealthy
}

func (p *HyperlightDevicePlugin) checkReadiness(ctx context.Context) error {
	p.mu.Lock()
	if p.checkGate == nil {
		p.checkGate = make(chan struct{}, 1)
	}
	gate := p.checkGate
	p.mu.Unlock()
	select {
	case gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-gate }()
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	probe := p.probe
	p.mu.Unlock()
	err := probe(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil && !p.deviceAccess {
		err = reconcileCDI(p.cdiPath, p.cdiSpec)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	previous := p.deviceHealthLocked()
	p.readinessErr = err
	p.readinessKnown = true
	if p.changed == nil {
		p.changed = make(chan struct{})
	}
	if previous != p.deviceHealthLocked() {
		close(p.changed)
		p.changed = make(chan struct{})
	}
	p.updateReadinessLocked()
	if err != nil {
		klog.Warningf("Allocation readiness failed: %v", err)
	}
	return err
}

// reconcileCDI owns only the configured file; temporary files have no CDI extension.
func reconcileCDI(path string, desired []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	matches, err := matchesCDI(path, desired)
	if err != nil {
		return err
	}
	if matches {
		return nil
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".hyperlight-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if err := temp.Chmod(0644); err != nil {
		return err
	}
	if _, err := temp.Write(desired); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return err
	}
	matches, err = matchesCDI(path, desired)
	if err != nil {
		return err
	}
	if !matches {
		return fmt.Errorf("CDI validation failed after replacement")
	}
	klog.Infof("Repaired CDI specification %s", path)
	return nil
}

func matchesCDI(path string, desired []byte) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("refusing non-regular CDI path %s", path)
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return false, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return false, fmt.Errorf("CDI file changed during validation")
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(len(desired)+1)))
	if err != nil {
		return false, err
	}
	return bytes.Equal(data, desired) && info.Mode().Perm() == 0644, nil
}

// newFSWatcher creates a filesystem watcher for kubelet restart detection.
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
// This function blocks until it detects our plugin socket being deleted.
func (p *HyperlightDevicePlugin) watchKubeletRestart() {
	klog.Info("Watching for kubelet restart using fsnotify...")

	watcher, err := newFSWatcher(filepath.Dir(p.socket))
	if err != nil {
		klog.Errorf("Failed to create fsnotify watcher, falling back to polling: %v", err)
		p.watchKubeletRestartPolling()
		return
	}
	defer watcher.Close()
	if _, err := os.Stat(p.socket); os.IsNotExist(err) {
		return
	}

	for {
		select {
		case <-p.stopCh:
			return
		case event, ok := <-watcher.Events:
			if !ok {
				klog.Warning("fsnotify events channel closed, falling back to polling")
				p.watchKubeletRestartPolling()
				return
			}
			if event.Name == p.socket && (event.Op&fsnotify.Remove) == fsnotify.Remove {
				klog.Info("Plugin socket deleted - kubelet may have restarted")
				return
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				klog.Warning("fsnotify errors channel closed, falling back to polling")
				p.watchKubeletRestartPolling()
				return
			}
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
			if _, err := os.Stat(p.socket); os.IsNotExist(err) {
				klog.Info("Plugin socket deleted - kubelet may have restarted")
				return
			}
		}
	}
}

func main() {
	klog.InitFlags(nil)
	deviceAccess := flag.Bool("device-access", false, "register one bootstrap hypervisor device grant for the main plugin")
	healthCheck := flag.String("health-check", "", "check liveness or readiness over the plugin socket")
	probeHypervisor := flag.String("probe-hypervisor", "", "internal bounded device probe")
	probePath := flag.String("probe-path", "", "internal device probe path")
	probeHostPath := flag.String("probe-host-path", "", "internal host device identity path")
	flag.Parse()
	defer klog.Flush()
	if *probeHypervisor != "" {
		if err := checkDeviceWithHost(*probeHypervisor, *probePath, *probeHostPath); err != nil {
			klog.Error(err)
			os.Exit(1)
		}
		return
	}
	if *healthCheck != "" {
		socket := serverSock
		if *deviceAccess {
			socket = deviceAccessSock
		}
		if err := checkHealth(*healthCheck, socket); err != nil {
			klog.Error(err)
			os.Exit(1)
		}
		return
	}
	plugin, err := newDevicePlugin(*deviceAccess)
	if err != nil {
		klog.Fatalf("Failed to create device plugin: %v", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	for ctx.Err() == nil {
		if err := plugin.Start(); err != nil {
			klog.Errorf("Failed to start device plugin: %v", err)
		} else {
			done := make(chan struct{})
			go func() { plugin.watchKubeletRestart(); close(done) }()
			select {
			case <-ctx.Done():
			case <-done:
			}
			plugin.Stop()
			<-done
		}
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
}

func checkHealth(service, socket string) error {
	if service != "liveness" && service != "readiness" {
		return fmt.Errorf("unknown health service %q", service)
	}
	if service == "readiness" {
		service = ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := dialPlugin(ctx, socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	response, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{Service: service})
	if err != nil {
		return err
	}
	if response.Status != healthpb.HealthCheckResponse_SERVING {
		return fmt.Errorf("plugin is not serving %s", service)
	}
	return nil
}

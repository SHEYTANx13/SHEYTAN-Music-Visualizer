package render

// gpu_adapter_class_test.go — honest adapter classification fixtures
// (run 42231355353 GPU evidence repair). The runner reported
// `adapter=Microsoft Basic Render Driver devtype=integrated-gpu
// api=dx12` and the pipeline accepted it as a real GPU. These tests
// pin the property-based classification: a software adapter is never
// hardware, whatever its device type or display name; a physical
// adapter needs properties, not a name.

import (
	"testing"

	"github.com/gogpu/gputypes"
)

func TestClassifySoftwareAdapterByMicrosoftVendorID(t *testing.T) {
	// The run-42231355353 runner case: DXGI's software adapter as the
	// D3D layer mislabels it — dx12 backend, "integrated-gpu" type,
	// Microsoft Basic Render Driver, PCI 0x1414/0x008c.
	info := gputypes.AdapterInfo{
		Name:       "Microsoft Basic Render Driver",
		Vendor:     "Microsoft",
		VendorID:   0x1414,
		DeviceID:   0x008c,
		DeviceType: gputypes.DeviceTypeIntegratedGPU,
		Backend:    gputypes.BackendDX12,
	}
	if got := classifyAdapterInfo(info); got != DeviceClassSoftware {
		t.Fatalf("Microsoft software adapter (0x1414/0x008c, type mislabeled) classified %q, want software", got)
	}
}

func TestClassifySoftwareAdapterByDeviceTypeCPU(t *testing.T) {
	info := gputypes.AdapterInfo{
		Name:       "Some Adapter",
		DeviceType: gputypes.DeviceTypeCPU,
		Backend:    gputypes.BackendVulkan,
	}
	if got := classifyAdapterInfo(info); got != DeviceClassSoftware {
		t.Fatalf("DeviceTypeCPU adapter classified %q, want software", got)
	}
}

func TestClassifySoftwareRasterizerNames(t *testing.T) {
	for _, name := range []string{"llvmpipe (LLVM 17.0.6)", "Mesa lavapipe", "SwiftShader", "softpipe"} {
		info := gputypes.AdapterInfo{
			Name:       name,
			DeviceType: gputypes.DeviceTypeIntegratedGPU,
			Backend:    gputypes.BackendVulkan,
		}
		if got := classifyAdapterInfo(info); got != DeviceClassSoftware {
			t.Fatalf("software rasterizer %q classified %q, want software", name, got)
		}
	}
}

func TestClassifyBasicRenderDriverNameAloneIsNotHardware(t *testing.T) {
	// A physical primary adapter with a similar name and a broken
	// display driver shares the software adapter's name. Without
	// property evidence (no PCI ids, unknown type) the class must be
	// unknown — never hardware, never blindly software either.
	info := gputypes.AdapterInfo{
		Name:       "Microsoft Basic Render Driver",
		DeviceType: gputypes.DeviceTypeOther,
		Backend:    gputypes.BackendDX12,
	}
	if got := classifyAdapterInfo(info); got != DeviceClassUnknown {
		t.Fatalf("name-only Basic Render Driver classified %q, want unknown", got)
	}
}

func TestClassifyHardwareAdapterByProperties(t *testing.T) {
	info := gputypes.AdapterInfo{
		Name:       "NVIDIA GeForce RTX 4070",
		Vendor:     "NVIDIA",
		VendorID:   0x10de,
		DeviceID:   0x2786,
		DeviceType: gputypes.DeviceTypeDiscreteGPU,
		Backend:    gputypes.BackendVulkan,
	}
	if got := classifyAdapterInfo(info); got != DeviceClassHardware {
		t.Fatalf("physical discrete adapter classified %q, want hardware", got)
	}
	info.Backend = gputypes.BackendDX12
	if got := classifyAdapterInfo(info); got != DeviceClassHardware {
		t.Fatalf("physical discrete dx12 adapter classified %q, want hardware", got)
	}
}

func TestClassifyVirtualGPUIsNeverHardware(t *testing.T) {
	info := gputypes.AdapterInfo{
		Name:       "VMware SVGA 3D",
		VendorID:   0x15ad,
		DeviceType: gputypes.DeviceTypeVirtualGPU,
		Backend:    gputypes.BackendVulkan,
	}
	if got := classifyAdapterInfo(info); got != DeviceClassVirtual {
		t.Fatalf("virtual adapter classified %q, want virtual", got)
	}
}

func TestClassifyUnknownPropertiesStayUnknown(t *testing.T) {
	info := gputypes.AdapterInfo{
		Name:       "Unknown Adapter",
		DeviceType: gputypes.DeviceTypeOther,
		Backend:    gputypes.BackendVulkan,
	}
	if got := classifyAdapterInfo(info); got != DeviceClassUnknown {
		t.Fatalf("unidentifiable adapter classified %q, want unknown", got)
	}
}

func TestClassifyNoopBackendIsSoftware(t *testing.T) {
	info := gputypes.AdapterInfo{
		Name:       "GoGPU Noop Adapter",
		DeviceType: gputypes.DeviceTypeOther,
		Backend:    gputypes.BackendEmpty,
	}
	if got := classifyAdapterInfo(info); got != DeviceClassSoftware {
		t.Fatalf("noop/empty-backend adapter classified %q, want software", got)
	}
}

func TestDeviceClassConstantsAreStable(t *testing.T) {
	// The self-check and consolidated report print these values; they
	// are part of the evidence contract and must not drift silently.
	for class, want := range map[string]string{
		DeviceClassHardware: "hardware",
		DeviceClassSoftware: "software",
		DeviceClassVirtual:  "virtual",
		DeviceClassUnknown:  "unknown",
	} {
		if class != want {
			t.Fatalf("device class constant drifted: %q != %q", class, want)
		}
	}
}

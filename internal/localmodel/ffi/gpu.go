package ffi

import "unsafe"

// DeviceID identifies a compute device (GPU, etc.)
type DeviceID struct {
	// ID is an identifier for the device for matching with system management libraries.
	ID string `json:"id"`

	// Library identifies which library is used for the device (e.g. CUDA, ROCm, etc.)
	Library string `json:"backend,omitempty"`
}

// Devices represents a compute device with its llama.cpp backend index.
type Devices struct {
	DeviceID
	LlamaID uint64
}

// EnumerateGPUs returns all GPU/iGPU devices discovered by ggml backends.
func EnumerateGPUs(lib *Library) []Devices {
	var ids []Devices

	count := lib.fnGgmlBackendDevCount()
	for i := uintptr(0); i < count; i++ {
		device := lib.fnGgmlBackendDevGet(i)
		devType := lib.fnGgmlBackendDevType(device)

		if devType == GGMLBackendDeviceTypeGPU || devType == GGMLBackendDeviceTypeIGPU {
			var props cBackendDevProps
			lib.fnGgmlBackendDevGetProps(device, uintptr(unsafe.Pointer(&props)))
			ids = append(ids, Devices{
				DeviceID: DeviceID{
					ID:      gostrN(props.ID[:]),
					Library: gostrN(props.Library[:]),
				},
				LlamaID: uint64(i),
			})
		}
	}
	return ids
}

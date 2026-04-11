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

			// Name contains the backend+index (e.g. "CUDA0"), which
			// we expose as the Library field for backend identification.
			name := gostr((*byte)(unsafe.Pointer(props.Name)))

			// DeviceID is the PCI bus id (e.g. "0000:01:00.0") or empty.
			var devID string
			if props.DeviceID != 0 {
				devID = gostr((*byte)(unsafe.Pointer(props.DeviceID)))
			}

			ids = append(ids, Devices{
				DeviceID: DeviceID{
					ID:      devID,
					Library: name,
				},
				LlamaID: uint64(i),
			})
		}
	}
	return ids
}

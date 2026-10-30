package nettrace

import (
	"cmp"
	"encoding/binary"
	"slices"
	"sort"
)

// decodeRuntimeEvent reads the loader and JIT events that name code, keyed by the provider's own manifest: the
// runtime and rundown providers number their events differently (runtime 154 is AssemblyLoad, rundown 154 is
// ModuleDCEnd). Events that only announce an unload or a start are ignored: whatever is live when the session
// ends is named by its load or DCEnd event.
func decodeRuntimeEvent(trace *Trace, provider string, eventID uint32, payload []byte) {
	switch provider {
	case ProviderRuntime:
		switch eventID {
		case 143: // MethodLoadVerbose
			decodeMethodRuntime(trace, payload)
		case 152: // ModuleLoad
			decodeModuleRuntime(trace, payload)
		case 154: // AssemblyLoad
			decodeAssemblyRuntime(trace, payload)
		}
	case providerRundown:
		switch eventID {
		case 143, 144: // MethodDCStartVerbose, MethodDCEndVerbose
			decodeMethodRuntime(trace, payload)
		case 154: // ModuleDCEnd
			decodeModuleRuntime(trace, payload)
		case 156: // AssemblyDCEnd
			decodeAssemblyRuntime(trace, payload)
		}
	}
}

func decodeMethodRuntime(trace *Trace, payload []byte) {
	if len(payload) < 24 {
		return
	}
	moduleID := binary.LittleEndian.Uint64(payload[8:])
	startAddress := binary.LittleEndian.Uint64(payload[16:])
	sizeOffset := 24
	if sizeOffset+12 > len(payload) {
		return
	}
	size := uint64(binary.LittleEndian.Uint32(payload[sizeOffset:]))
	if startAddress == 0 || size == 0 {
		return
	}
	offset := sizeOffset + 12
	namespace, ok := readWideNull(payload, &offset)
	if !ok {
		return
	}
	name, ok := readWideNull(payload, &offset)
	if !ok {
		return
	}
	trace.Methods = append(trace.Methods, Method{
		StartAddress: startAddress,
		Size:         size,
		ModuleID:     moduleID,
		Namespace:    namespace,
		Name:         name,
	})
}

func decodeModuleRuntime(trace *Trace, payload []byte) {
	if len(payload) < 16 {
		return
	}
	moduleID := binary.LittleEndian.Uint64(payload)
	assemblyID := binary.LittleEndian.Uint64(payload[8:])
	if moduleID != 0 && assemblyID != 0 {
		trace.Modules[moduleID] = assemblyID
	}
}

func decodeAssemblyRuntime(trace *Trace, payload []byte) {
	if len(payload) < 8 {
		return
	}
	assemblyID := binary.LittleEndian.Uint64(payload)
	if assemblyID == 0 {
		return
	}
	// AssemblyID, AppDomainID, BindingID, AssemblyFlags u32, then the name.
	offset := 28
	if name, ok := readWideNull(payload, &offset); ok && name != "" {
		trace.Assemblies[assemblyID] = name
	}
}

func resolveMethods(trace *Trace) {
	for i := range trace.Methods {
		if assemblyID := trace.Modules[trace.Methods[i].ModuleID]; assemblyID != 0 {
			trace.Methods[i].Assembly = trace.Assemblies[assemblyID]
		}
	}
	// A startup trace holds about a million samples and tens of thousands of methods, so frames are matched by binary
	// search over the methods sorted by start address.
	byStart := slices.Clone(trace.Methods)
	slices.SortStableFunc(byStart, func(a, b Method) int { return cmp.Compare(a.StartAddress, b.StartAddress) })
	done := make(map[*Frame]bool)
	for sampleIndex := range trace.Samples {
		stack := trace.Samples[sampleIndex].Frames
		if len(stack) == 0 || done[&stack[0]] {
			continue
		}
		done[&stack[0]] = true
		for frameIndex := range stack {
			frame := &stack[frameIndex]
			// The candidate is the last method starting at or before the frame's address.
			next := sort.Search(len(byStart), func(i int) bool { return byStart[i].StartAddress > frame.IP })
			if next == 0 {
				continue
			}
			method := byStart[next-1]
			if frame.IP >= method.StartAddress+method.Size {
				continue
			}
			frame.Method = method.Name
			frame.Type = method.Namespace
			frame.Assembly = method.Assembly
		}
	}
}

func readVarint(data []byte) (uint64, int) {
	var value uint64
	for i, b := range data {
		if i >= 10 {
			return 0, 0
		}
		value |= uint64(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			return value, i + 1
		}
	}
	return 0, 0
}

func uint32FromUint64(value uint64) uint32 {
	const maxUint32 = uint64(1<<32 - 1)
	if value > maxUint32 {
		return ^uint32(0)
	}
	return uint32(value)
}

func intFromUint64(value uint64) int {
	maxInt := uint64(^uint(0) >> 1)
	if value > maxInt {
		return int(maxInt)
	}
	return int(value)
}

func uint64FromInt(value int) uint64 {
	if value < 0 {
		return 0
	}
	return uint64(value)
}

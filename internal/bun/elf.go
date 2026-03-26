package bun

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	overlayTrailer = "\n---- Bun! ----\n"
	overlayOffsets = 32
	moduleSize     = 52
)

type Format string

const (
	FormatSection Format = "section"
	FormatOverlay Format = "overlay"
)

type Metadata struct {
	Format            Format
	PayloadOffset     int64
	PayloadSize       int
	PayloadCapacity   int
	SectionDataOffset int64
	SectionDataSize   int
	DeclaredSize      uint64
	OffsetsOffset     int64
	TrailerOffset     int64
	TotalCountOffset  int64
}

type Bundle struct {
	Metadata Metadata
	Payload  []byte
}

type StringPointer struct {
	Offset uint32
	Length uint32
}

type Module struct {
	Name               StringPointer
	Contents           StringPointer
	Sourcemap          StringPointer
	Bytecode           StringPointer
	ModuleInfo         StringPointer
	BytecodeOriginPath StringPointer
	Encoding           byte
	Loader             byte
	ModuleFormat       byte
	Side               byte
}

type Offsets struct {
	ByteCount          uint64
	ModulesPtr         StringPointer
	EntryPointID       uint32
	CompileExecArgvPtr StringPointer
	Flags              uint32
}

type ModuleGraph struct {
	Format     Format
	GraphBytes []byte
	Offsets    Offsets
	Modules    []Module
}

func Extract(data []byte) (*Bundle, error) {
	meta, err := ParseELFSectionMetadata(data)
	if err == nil {
		return extractAt(data, meta)
	}

	overlayMeta, overlayErr := ParseOverlayMetadata(data)
	if overlayErr == nil {
		return extractAt(data, overlayMeta)
	}

	return nil, errors.Join(err, overlayErr)
}

func ParseELFSectionMetadata(data []byte) (Metadata, error) {
	file, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		return Metadata{}, fmt.Errorf("open ELF: %w", err)
	}
	defer file.Close()

	section := file.Section(".bun")
	if section == nil {
		return Metadata{}, errors.New("missing .bun section")
	}

	sectionData, err := section.Data()
	if err != nil {
		return Metadata{}, fmt.Errorf("read .bun section: %w", err)
	}
	if len(sectionData) < 8 {
		return Metadata{}, errors.New(".bun section too small")
	}

	declared := binary.LittleEndian.Uint64(sectionData[:8])
	if declared == 0 {
		return Metadata{}, errors.New(".bun declared payload length is zero")
	}
	if declared > uint64(len(sectionData)-8) {
		return Metadata{}, fmt.Errorf(".bun declared payload length %d exceeds section bytes %d", declared, len(sectionData)-8)
	}

	return Metadata{
		Format:            FormatSection,
		PayloadOffset:     int64(section.Offset) + 8,
		PayloadSize:       int(declared),
		PayloadCapacity:   len(sectionData) - 8,
		SectionDataOffset: int64(section.Offset),
		SectionDataSize:   len(sectionData),
		DeclaredSize:      declared,
	}, nil
}

func ParseOverlayMetadata(data []byte) (Metadata, error) {
	trailer := []byte(overlayTrailer)
	trailerOffset := bytes.LastIndex(data, trailer)
	if trailerOffset < 0 {
		return Metadata{}, errors.New("missing Bun overlay trailer")
	}

	totalCountOffset := trailerOffset + len(trailer)
	if totalCountOffset+8 > len(data) {
		return Metadata{}, errors.New("truncated Bun overlay total byte count")
	}

	offsetsOffset := trailerOffset - overlayOffsets
	if offsetsOffset < 0 {
		return Metadata{}, errors.New("truncated Bun overlay offsets struct")
	}

	totalCount := binary.LittleEndian.Uint64(data[totalCountOffset : totalCountOffset+8])
	declaredCount := binary.LittleEndian.Uint64(data[offsetsOffset : offsetsOffset+8])
	if declaredCount != totalCount {
		return Metadata{}, fmt.Errorf("overlay byte count mismatch: offsets=%d trailer=%d", declaredCount, totalCount)
	}

	payloadOffset := offsetsOffset - int(totalCount)
	if payloadOffset < 0 {
		return Metadata{}, errors.New("invalid Bun overlay payload offset")
	}

	payloadSize := int(totalCount) + overlayOffsets + len(trailer) + 8
	return Metadata{
		Format:           FormatOverlay,
		PayloadOffset:    int64(payloadOffset),
		PayloadSize:      payloadSize,
		PayloadCapacity:  payloadSize,
		OffsetsOffset:    int64(offsetsOffset - payloadOffset),
		TrailerOffset:    int64(trailerOffset - payloadOffset),
		TotalCountOffset: int64(totalCountOffset - payloadOffset),
		DeclaredSize:     totalCount,
	}, nil
}

func ParseModuleGraph(format Format, payload []byte) (*ModuleGraph, error) {
	var (
		offsetsOffset int
		byteCount     uint64
		offsets       Offsets
	)

	switch format {
	case FormatSection:
		trailer := []byte(overlayTrailer)
		trailerOffset := bytes.LastIndex(payload, trailer)
		if trailerOffset < 0 {
			return nil, errors.New("missing Bun section trailer")
		}
		offsetsOffset = trailerOffset - overlayOffsets
		if offsetsOffset < 0 {
			return nil, errors.New("truncated Bun section offsets struct")
		}
		offsets = decodeOffsets(payload[offsetsOffset : offsetsOffset+overlayOffsets])
		byteCount = offsets.ByteCount
		if trailerOffset != int(byteCount)+overlayOffsets {
			return nil, fmt.Errorf("section trailer offset %d does not follow byte_count %d", trailerOffset, byteCount)
		}
	case FormatOverlay:
		trailerOffset := len(payload) - len(overlayTrailer) - 8
		if trailerOffset < overlayOffsets {
			return nil, errors.New("truncated Bun overlay payload")
		}
		offsetsOffset = trailerOffset - overlayOffsets
		offsets = decodeOffsets(payload[offsetsOffset : offsetsOffset+overlayOffsets])
		byteCount = offsets.ByteCount
	default:
		return nil, fmt.Errorf("unsupported format %q", format)
	}

	if int(byteCount) > offsetsOffset {
		return nil, fmt.Errorf("byte_count %d exceeds offsets start %d", byteCount, offsetsOffset)
	}

	graphBytes := append([]byte(nil), payload[:byteCount]...)
	modules, err := decodeModules(graphBytes, offsets.ModulesPtr)
	if err != nil {
		return nil, err
	}

	return &ModuleGraph{
		Format:     format,
		GraphBytes: graphBytes,
		Offsets:    offsets,
		Modules:    modules,
	}, nil
}

func (g *ModuleGraph) EntryPointModule() (int, Module, error) {
	index := int(g.Offsets.EntryPointID)
	if index < 0 || index >= len(g.Modules) {
		return -1, Module{}, fmt.Errorf("entry point id %d out of range", g.Offsets.EntryPointID)
	}
	return index, g.Modules[index], nil
}

func (g *ModuleGraph) Slice(pointer StringPointer) ([]byte, error) {
	start := int(pointer.Offset)
	end := start + int(pointer.Length)
	if start < 0 || end > len(g.GraphBytes) {
		return nil, fmt.Errorf("pointer %d:%d outside graph byte range %d", pointer.Offset, pointer.Length, len(g.GraphBytes))
	}
	return g.GraphBytes[start:end], nil
}

func (g *ModuleGraph) ReplaceModuleContents(index int, newContents []byte) ([]byte, error) {
	if index < 0 || index >= len(g.Modules) {
		return nil, fmt.Errorf("module index %d out of range", index)
	}

	module := g.Modules[index]
	oldStart := int(module.Contents.Offset)
	oldEnd := oldStart + int(module.Contents.Length)
	if oldStart < 0 || oldEnd > len(g.GraphBytes) {
		return nil, errors.New("module contents pointer outside graph bytes")
	}

	newGraphBytes := make([]byte, 0, len(g.GraphBytes)-int(module.Contents.Length)+len(newContents))
	newGraphBytes = append(newGraphBytes, g.GraphBytes[:oldStart]...)
	newGraphBytes = append(newGraphBytes, newContents...)
	newGraphBytes = append(newGraphBytes, g.GraphBytes[oldEnd:]...)

	delta := len(newContents) - int(module.Contents.Length)
	modules := append([]Module(nil), g.Modules...)
	modules[index].Contents.Length = uint32(len(newContents))

	for i := range modules {
		modules[i].Name = shiftPointer(modules[i].Name, oldStart, delta)
		modules[i].Contents = shiftPointer(modules[i].Contents, oldStart, delta)
		modules[i].Sourcemap = shiftPointer(modules[i].Sourcemap, oldStart, delta)
		modules[i].Bytecode = shiftPointer(modules[i].Bytecode, oldStart, delta)
		modules[i].ModuleInfo = shiftPointer(modules[i].ModuleInfo, oldStart, delta)
		modules[i].BytecodeOriginPath = shiftPointer(modules[i].BytecodeOriginPath, oldStart, delta)
	}
	modules[index].Contents.Offset = uint32(oldStart)

	offsets := g.Offsets
	offsets.ModulesPtr = shiftPointer(offsets.ModulesPtr, oldStart, delta)
	offsets.CompileExecArgvPtr = shiftPointer(offsets.CompileExecArgvPtr, oldStart, delta)
	offsets.ByteCount = uint64(len(newGraphBytes))

	modulesOffset := int(offsets.ModulesPtr.Offset)
	modulesLength := int(offsets.ModulesPtr.Length)
	if modulesLength != len(modules)*moduleSize {
		return nil, fmt.Errorf("unexpected modules pointer length %d for %d modules", modulesLength, len(modules))
	}
	if modulesOffset < 0 || modulesOffset+modulesLength > len(newGraphBytes) {
		return nil, errors.New("shifted modules pointer outside graph bytes")
	}

	encodedModules := encodeModules(modules)
	copy(newGraphBytes[modulesOffset:modulesOffset+len(encodedModules)], encodedModules)
	return rebuildPayload(g.Format, newGraphBytes, offsets), nil
}

func ReplacePayload(data []byte, meta Metadata, newPayload []byte) ([]byte, error) {
	if len(newPayload) > meta.PayloadCapacity {
		return nil, fmt.Errorf("replacement payload length %d exceeds payload capacity %d", len(newPayload), meta.PayloadCapacity)
	}

	out := append([]byte(nil), data...)
	start := int(meta.PayloadOffset)
	end := start + meta.PayloadSize
	if start < 0 || end > len(out) {
		return nil, errors.New("payload range is out of bounds")
	}

	switch meta.Format {
	case FormatSection:
		copy(out[start:start+len(newPayload)], newPayload)
		for i := start + len(newPayload); i < start+meta.PayloadCapacity; i++ {
			out[i] = 0
		}
		binary.LittleEndian.PutUint64(out[int(meta.SectionDataOffset):int(meta.SectionDataOffset)+8], uint64(len(newPayload)))
	case FormatOverlay:
		prefix := out[:start]
		suffixStart := start + meta.PayloadSize
		suffix := out[suffixStart:]
		rebuilt := make([]byte, 0, len(prefix)+len(newPayload)+len(suffix))
		rebuilt = append(rebuilt, prefix...)
		rebuilt = append(rebuilt, newPayload...)
		rebuilt = append(rebuilt, suffix...)
		out = rebuilt
	default:
		return nil, fmt.Errorf("unsupported container format %q", meta.Format)
	}

	return out, nil
}

func extractAt(data []byte, meta Metadata) (*Bundle, error) {
	start := int(meta.PayloadOffset)
	end := start + meta.PayloadSize
	if start < 0 || end > len(data) {
		return nil, errors.New("payload metadata points outside file")
	}
	return &Bundle{
		Metadata: meta,
		Payload:  append([]byte(nil), data[start:end]...),
	}, nil
}

func decodeOffsets(data []byte) Offsets {
	return Offsets{
		ByteCount:          binary.LittleEndian.Uint64(data[0:8]),
		ModulesPtr:         decodePointer(data[8:16]),
		EntryPointID:       binary.LittleEndian.Uint32(data[16:20]),
		CompileExecArgvPtr: decodePointer(data[20:28]),
		Flags:              binary.LittleEndian.Uint32(data[28:32]),
	}
}

func decodePointer(data []byte) StringPointer {
	return StringPointer{
		Offset: binary.LittleEndian.Uint32(data[0:4]),
		Length: binary.LittleEndian.Uint32(data[4:8]),
	}
}

func decodeModules(graphBytes []byte, modulesPtr StringPointer) ([]Module, error) {
	if modulesPtr.Length%moduleSize != 0 {
		return nil, fmt.Errorf("module pointer length %d is not aligned to module size %d", modulesPtr.Length, moduleSize)
	}
	start := int(modulesPtr.Offset)
	end := start + int(modulesPtr.Length)
	if start < 0 || end > len(graphBytes) {
		return nil, fmt.Errorf("modules pointer %d:%d outside graph byte range %d", modulesPtr.Offset, modulesPtr.Length, len(graphBytes))
	}

	raw := graphBytes[start:end]
	modules := make([]Module, 0, len(raw)/moduleSize)
	for i := 0; i < len(raw); i += moduleSize {
		modules = append(modules, Module{
			Name:               decodePointer(raw[i : i+8]),
			Contents:           decodePointer(raw[i+8 : i+16]),
			Sourcemap:          decodePointer(raw[i+16 : i+24]),
			Bytecode:           decodePointer(raw[i+24 : i+32]),
			ModuleInfo:         decodePointer(raw[i+32 : i+40]),
			BytecodeOriginPath: decodePointer(raw[i+40 : i+48]),
			Encoding:           raw[i+48],
			Loader:             raw[i+49],
			ModuleFormat:       raw[i+50],
			Side:               raw[i+51],
		})
	}
	return modules, nil
}

func encodeModules(modules []Module) []byte {
	out := make([]byte, 0, len(modules)*moduleSize)
	for _, module := range modules {
		var encoded [moduleSize]byte
		encodePointer(encoded[0:8], module.Name)
		encodePointer(encoded[8:16], module.Contents)
		encodePointer(encoded[16:24], module.Sourcemap)
		encodePointer(encoded[24:32], module.Bytecode)
		encodePointer(encoded[32:40], module.ModuleInfo)
		encodePointer(encoded[40:48], module.BytecodeOriginPath)
		encoded[48] = module.Encoding
		encoded[49] = module.Loader
		encoded[50] = module.ModuleFormat
		encoded[51] = module.Side
		out = append(out, encoded[:]...)
	}
	return out
}

func encodePointer(dst []byte, pointer StringPointer) {
	binary.LittleEndian.PutUint32(dst[0:4], pointer.Offset)
	binary.LittleEndian.PutUint32(dst[4:8], pointer.Length)
}

func shiftPointer(pointer StringPointer, replacedOffset int, delta int) StringPointer {
	if pointer.Length == 0 {
		return pointer
	}
	if int(pointer.Offset) > replacedOffset {
		pointer.Offset = uint32(int(pointer.Offset) + delta)
	}
	return pointer
}

func rebuildPayload(format Format, graphBytes []byte, offsets Offsets) []byte {
	offsets.ByteCount = uint64(len(graphBytes))
	offsetBytes := make([]byte, overlayOffsets)
	binary.LittleEndian.PutUint64(offsetBytes[0:8], offsets.ByteCount)
	encodePointer(offsetBytes[8:16], offsets.ModulesPtr)
	binary.LittleEndian.PutUint32(offsetBytes[16:20], offsets.EntryPointID)
	encodePointer(offsetBytes[20:28], offsets.CompileExecArgvPtr)
	binary.LittleEndian.PutUint32(offsetBytes[28:32], offsets.Flags)

	var out bytes.Buffer
	out.Write(graphBytes)
	out.Write(offsetBytes)
	out.WriteString(overlayTrailer)
	if format == FormatOverlay {
		var totalCount [8]byte
		binary.LittleEndian.PutUint64(totalCount[:], uint64(len(graphBytes)))
		out.Write(totalCount[:])
	}
	return out.Bytes()
}

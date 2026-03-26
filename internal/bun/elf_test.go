package bun

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestParseELFSectionMetadata(t *testing.T) {
	payload := []byte("console.log('bun section');")
	image := buildMinimalELFWithBunSection(t, payload)

	meta, err := ParseELFSectionMetadata(image)
	if err != nil {
		t.Fatalf("ParseELFSectionMetadata failed: %v", err)
	}
	if meta.Format != FormatSection {
		t.Fatalf("expected section format, got %s", meta.Format)
	}
	if meta.PayloadSize != len(payload) {
		t.Fatalf("expected payload size %d, got %d", len(payload), meta.PayloadSize)
	}
}

func TestParseOverlayMetadata(t *testing.T) {
	payload := []byte("console.log('overlay payload');")
	image := buildOverlayFixture(payload)

	meta, err := ParseOverlayMetadata(image)
	if err != nil {
		t.Fatalf("ParseOverlayMetadata failed: %v", err)
	}
	if meta.Format != FormatOverlay {
		t.Fatalf("expected overlay format, got %s", meta.Format)
	}
	expectedSize := len(payload) + overlayOffsets + len(overlayTrailer) + 8
	if meta.PayloadSize != expectedSize {
		t.Fatalf("expected payload size %d, got %d", expectedSize, meta.PayloadSize)
	}
}

func TestReplacePayloadSection(t *testing.T) {
	payload := []byte("console.log('bun section');")
	image := buildMinimalELFWithBunSection(t, payload)
	meta, err := ParseELFSectionMetadata(image)
	if err != nil {
		t.Fatalf("ParseELFSectionMetadata failed: %v", err)
	}

	replaced, err := ReplacePayload(image, meta, []byte("console.log('bun replace');"))
	if err != nil {
		t.Fatalf("ReplacePayload failed: %v", err)
	}
	extracted, err := Extract(replaced)
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}
	if !bytes.Equal(extracted.Payload, []byte("console.log('bun replace');")) {
		t.Fatalf("unexpected replaced payload %q", extracted.Payload)
	}
}

func TestReplacePayloadOverlayRoundTrip(t *testing.T) {
	originalPayload := buildOverlayGraphPayload([]byte(`VERSION:"2.1.84";console.log("overlay");`))
	image := buildOverlayFixture(originalPayload)

	meta, err := ParseOverlayMetadata(image)
	if err != nil {
		t.Fatalf("ParseOverlayMetadata failed: %v", err)
	}

	replacementPayload := buildOverlayGraphPayload([]byte(`VERSION:"2.1.84";console.log("overlay replacement with longer contents");`))
	replaced, err := ReplacePayload(image, meta, replacementPayload)
	if err != nil {
		t.Fatalf("ReplacePayload failed: %v", err)
	}
	extracted, err := Extract(replaced)
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}
	if !bytes.Equal(extracted.Payload, replacementPayload) {
		t.Fatalf("unexpected overlay payload after replace")
	}
	graph, err := ParseModuleGraph(FormatOverlay, extracted.Payload)
	if err != nil {
		t.Fatalf("ParseModuleGraph after overlay replace failed: %v", err)
	}
	_, entry, err := graph.EntryPointModule()
	if err != nil {
		t.Fatalf("EntryPointModule after overlay replace failed: %v", err)
	}
	contents, err := graph.Slice(entry.Contents)
	if err != nil {
		t.Fatalf("Slice after overlay replace failed: %v", err)
	}
	if !bytes.Contains(contents, []byte("replacement")) {
		t.Fatalf("expected replacement contents in overlay graph")
	}
}

func TestParseModuleGraphAndReplaceContents(t *testing.T) {
	unpatched := []byte(`VERSION:"2.1.84";` + `,G=dY.useCallback(()=>{if(j.current!==void 0)clearTimeout(j.current);j.current=setTimeout((k,N)=>{k.current=void 0,N()},300,j,J)},[J]);dY.useEffect(()=>{if($!==Y.current.messageId||D!==Y.current.permissionMode||A!==Y.current.vimMode)Y.current.permissionMode=D,Y.current.vimMode=A,G()},[$,D,A,G]);`)
	payload := buildSectionGraphPayload(unpatched)

	graph, err := ParseModuleGraph(FormatSection, payload)
	if err != nil {
		t.Fatalf("ParseModuleGraph failed: %v", err)
	}
	index, entry, err := graph.EntryPointModule()
	if err != nil {
		t.Fatalf("EntryPointModule failed: %v", err)
	}
	contents, err := graph.Slice(entry.Contents)
	if err != nil {
		t.Fatalf("Slice failed: %v", err)
	}
	if !bytes.Equal(contents, unpatched) {
		t.Fatalf("unexpected entry-point contents")
	}

	replacedPayload, err := graph.ReplaceModuleContents(index, bytes.Replace(contents, []byte("300"), []byte("1000"), 1))
	if err != nil {
		t.Fatalf("ReplaceModuleContents failed: %v", err)
	}
	reparsed, err := ParseModuleGraph(FormatSection, replacedPayload)
	if err != nil {
		t.Fatalf("ParseModuleGraph after replace failed: %v", err)
	}
	_, replacedEntry, err := reparsed.EntryPointModule()
	if err != nil {
		t.Fatalf("EntryPointModule after replace failed: %v", err)
	}
	replacedContents, err := reparsed.Slice(replacedEntry.Contents)
	if err != nil {
		t.Fatalf("Slice after replace failed: %v", err)
	}
	if !bytes.Contains(replacedContents, []byte("1000")) {
		t.Fatalf("expected replaced contents to contain updated interval")
	}
}

func TestShiftPointerAtExactOffset(t *testing.T) {
	shifted, err := shiftPointer(StringPointer{Offset: 32, Length: 4}, 32, 7)
	if err != nil {
		t.Fatalf("shiftPointer failed: %v", err)
	}
	if shifted.Offset != 39 {
		t.Fatalf("expected offset 39, got %d", shifted.Offset)
	}
}

func TestShiftPointerRejectsNegativeOffset(t *testing.T) {
	if _, err := shiftPointer(StringPointer{Offset: 5, Length: 4}, 0, -8); err == nil {
		t.Fatalf("expected underflow error")
	}
}

func buildOverlayFixture(payload []byte) []byte {
	offsets := make([]byte, overlayOffsets)
	binary.LittleEndian.PutUint64(offsets[:8], uint64(len(payload)))
	totalCount := make([]byte, 8)
	binary.LittleEndian.PutUint64(totalCount, uint64(len(payload)))

	var out bytes.Buffer
	out.WriteString("ELF-OVERLAY-TEST")
	out.Write(payload)
	out.Write(offsets)
	out.WriteString(overlayTrailer)
	out.Write(totalCount)
	return out.Bytes()
}

func buildSectionGraphPayload(contents []byte) []byte {
	name := []byte("/$bunfs/root/src/entrypoints/cli.js")
	origin := []byte("/$bunfs/root/src/entrypoints/cli.js")
	graph := make([]byte, 0, len(name)+len(contents)+len(origin)+moduleSize)

	namePtr := StringPointer{Offset: uint32(len(graph)), Length: uint32(len(name))}
	graph = append(graph, name...)
	contentsPtr := StringPointer{Offset: uint32(len(graph)), Length: uint32(len(contents))}
	graph = append(graph, contents...)
	originPtr := StringPointer{Offset: uint32(len(graph)), Length: uint32(len(origin))}
	graph = append(graph, origin...)

	modulesPtr := StringPointer{Offset: uint32(len(graph)), Length: moduleSize}
	module := Module{
		Name:               namePtr,
		Contents:           contentsPtr,
		BytecodeOriginPath: originPtr,
	}
	graph = append(graph, encodeModules([]Module{module})...)

	offsetBytes := make([]byte, overlayOffsets)
	binary.LittleEndian.PutUint64(offsetBytes[:8], uint64(len(graph)))
	encodePointer(offsetBytes[8:16], modulesPtr)
	binary.LittleEndian.PutUint32(offsetBytes[16:20], 0)
	encodePointer(offsetBytes[20:28], StringPointer{Offset: uint32(len(graph)), Length: 0})
	binary.LittleEndian.PutUint32(offsetBytes[28:32], 0)

	var out bytes.Buffer
	out.Write(graph)
	out.Write(offsetBytes)
	out.WriteString(overlayTrailer)
	return out.Bytes()
}

func buildOverlayGraphPayload(contents []byte) []byte {
	sectionPayload := buildSectionGraphPayload(contents)
	offsetsOffset := len(sectionPayload) - len(overlayTrailer) - overlayOffsets
	graphBytes := sectionPayload[:offsetsOffset]
	offsetBytes := sectionPayload[offsetsOffset : offsetsOffset+overlayOffsets]

	var out bytes.Buffer
	out.Write(graphBytes)
	out.Write(offsetBytes)
	out.WriteString(overlayTrailer)
	var totalCount [8]byte
	binary.LittleEndian.PutUint64(totalCount[:], uint64(len(graphBytes)))
	out.Write(totalCount[:])
	return out.Bytes()
}

func buildMinimalELFWithBunSection(t *testing.T, payload []byte) []byte {
	t.Helper()

	const (
		elfHeaderSize     = 64
		sectionHeaderSize = 64
		sectionCount      = 3
	)

	shstrtab := []byte{0, '.', 's', 'h', 's', 't', 'r', 't', 'a', 'b', 0, '.', 'b', 'u', 'n', 0}
	bunData := make([]byte, 8+len(payload))
	binary.LittleEndian.PutUint64(bunData[:8], uint64(len(payload)))
	copy(bunData[8:], payload)

	shstrtabOffset := elfHeaderSize
	bunOffset := alignUp(shstrtabOffset+len(shstrtab), 8)
	sectionHeadersOffset := alignUp(bunOffset+len(bunData), 8)
	totalSize := sectionHeadersOffset + (sectionHeaderSize * sectionCount)

	out := make([]byte, totalSize)
	copy(out[:4], []byte{0x7f, 'E', 'L', 'F'})
	out[4] = 2 // ELFCLASS64
	out[5] = 1 // little-endian
	out[6] = 1 // version

	binary.LittleEndian.PutUint16(out[16:], 2)  // ET_EXEC
	binary.LittleEndian.PutUint16(out[18:], 62) // EM_X86_64
	binary.LittleEndian.PutUint32(out[20:], 1)
	binary.LittleEndian.PutUint64(out[40:], uint64(sectionHeadersOffset))
	binary.LittleEndian.PutUint16(out[52:], elfHeaderSize)
	binary.LittleEndian.PutUint16(out[58:], sectionHeaderSize)
	binary.LittleEndian.PutUint16(out[60:], sectionCount)
	binary.LittleEndian.PutUint16(out[62:], 1) // .shstrtab index

	copy(out[shstrtabOffset:], shstrtab)
	copy(out[bunOffset:], bunData)

	shoff := sectionHeadersOffset
	writeSectionHeader(out[shoff+sectionHeaderSize:], 1, 3, 0, uint64(shstrtabOffset), uint64(len(shstrtab)), 1)
	writeSectionHeader(out[shoff+(sectionHeaderSize*2):], 11, 1, 0, uint64(bunOffset), uint64(len(bunData)), 1)

	return out
}

func writeSectionHeader(dst []byte, nameOffset uint32, sectionType uint32, flags uint64, offset uint64, size uint64, align uint64) {
	binary.LittleEndian.PutUint32(dst[0:], nameOffset)
	binary.LittleEndian.PutUint32(dst[4:], sectionType)
	binary.LittleEndian.PutUint64(dst[8:], flags)
	binary.LittleEndian.PutUint64(dst[24:], offset)
	binary.LittleEndian.PutUint64(dst[32:], size)
	binary.LittleEndian.PutUint64(dst[48:], align)
}

func alignUp(value int, align int) int {
	if value%align == 0 {
		return value
	}
	return value + (align - (value % align))
}

package patch

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	claudeMetadataVersionPattern = regexp.MustCompile(`PACKAGE_URL:"@anthropic-ai/claude-code"[\s\S]{0,200}?VERSION:"([0-9]+\.[0-9]+\.[0-9]+)"`)
	versionPattern               = regexp.MustCompile(`VERSION:"([0-9]+\.[0-9]+\.[0-9]+)"`)
)

const (
	ShapeIDStatuslineDebounceV1 = "statusline_debounce_v1"
	ShapeIDStatuslineDebounceV2 = "statusline_debounce_v2"
)

type State string

const (
	StateUnpatched         State = "unpatched"
	StatePatched           State = "patched"
	StateUnrecognizedShape State = "unrecognized_shape"
	StateAmbiguousShape    State = "ambiguous_shape"
)

type ShapeState string

const (
	ShapeStateKnown        ShapeState = "known"
	ShapeStateUnrecognized ShapeState = "unrecognized"
	ShapeStateAmbiguous    ShapeState = "ambiguous"
)

type PatchState string

const (
	PatchStateUnpatched PatchState = "unpatched"
	PatchStatePatched   PatchState = "patched"
	PatchStateUnknown   PatchState = "unknown"
)

type Inspection struct {
	State            State
	ShapeState       ShapeState
	PatchState       PatchState
	Version          string
	ShapeID          string
	ObservedVersions []string
	IntervalMS       int
	UnpatchedMatches int
	PatchedMatches   int

	selected    shapeMatch
	hasSelected bool
}

type compiledPattern struct {
	re         *regexp.Regexp
	groupIndex map[string]int
}

type shapeFamily struct {
	id                string
	observedVersions  []string
	unpatched         compiledPattern
	patched           compiledPattern
	validateUnpatched func([]byte, regexMatch) bool
	validatePatched   func([]byte, regexMatch) bool
	buildReplacement  func([]byte, shapeMatch, int) ([]byte, error)
}

type regexMatch struct {
	pattern compiledPattern
	idxs    []int
}

type shapeMatch struct {
	kind     PatchState
	shapeID  string
	start    int
	end      int
	match    regexMatch
	interval int
}

type scanResult struct {
	valid            int
	unpatchedMatches int
	patchedMatches   int
	malformed        bool
	first            shapeMatch
}

var (
	identifierPattern      = `[A-Za-z_$][A-Za-z0-9_$]*`
	shapeFamilies          = []shapeFamily{newStatuslineDebounceV1(), newStatuslineDebounceV2()}
	documentedLiveVerified = map[string]struct{}{"2.1.84": {}, "2.1.85": {}, "2.1.86": {}}
)

func Inspect(payload []byte) Inspection {
	version := DetectVersion(payload)
	scan := scanShapes(payload)

	inspection := Inspection{
		Version:          version,
		UnpatchedMatches: scan.unpatchedMatches,
		PatchedMatches:   scan.patchedMatches,
		PatchState:       PatchStateUnknown,
	}

	switch {
	case scan.malformed || scan.valid > 1:
		inspection.State = StateAmbiguousShape
		inspection.ShapeState = ShapeStateAmbiguous
	case scan.valid == 0:
		inspection.State = StateUnrecognizedShape
		inspection.ShapeState = ShapeStateUnrecognized
	default:
		inspection.ShapeState = ShapeStateKnown
		inspection.ShapeID = scan.first.shapeID
		inspection.ObservedVersions = append(inspection.ObservedVersions, observedVersionsForShape(scan.first.shapeID)...)
		inspection.PatchState = scan.first.kind
		inspection.selected = scan.first
		inspection.hasSelected = true
		switch scan.first.kind {
		case PatchStateUnpatched:
			inspection.State = StateUnpatched
		case PatchStatePatched:
			inspection.State = StatePatched
			inspection.IntervalMS = scan.first.interval
		default:
			inspection.State = StateUnrecognizedShape
			inspection.ShapeState = ShapeStateUnrecognized
			inspection.PatchState = PatchStateUnknown
			inspection.hasSelected = false
		}
	}

	return inspection
}

func DetectVersion(payload []byte) string {
	for _, pattern := range []*regexp.Regexp{claudeMetadataVersionPattern, versionPattern} {
		match := pattern.FindSubmatch(payload)
		if len(match) == 2 {
			return sanitizeFieldValue(string(match[1]))
		}
	}
	return ""
}

func IsDocumentedLiveVerifiedVersion(version string) bool {
	_, ok := documentedLiveVerified[version]
	return ok
}

func ObservedVersions(shapeID string) []string {
	return append([]string(nil), observedVersionsForShape(shapeID)...)
}

func Apply(payload []byte, intervalMS int) ([]byte, error) {
	inspection := Inspect(payload)
	switch inspection.State {
	case StateUnpatched:
		return ApplyInspection(payload, inspection, intervalMS)
	case StatePatched:
		return nil, fmt.Errorf("payload already patched at %dms", inspection.IntervalMS)
	case StateAmbiguousShape:
		return nil, fmt.Errorf("patch match is ambiguous")
	default:
		return nil, fmt.Errorf("payload shape is unrecognized for patching")
	}
}

func ApplyKnownUnpatched(payload []byte, intervalMS int) ([]byte, error) {
	return ApplyInspection(payload, Inspect(payload), intervalMS)
}

func ApplyInspection(payload []byte, inspection Inspection, intervalMS int) ([]byte, error) {
	if intervalMS <= 0 {
		return nil, fmt.Errorf("interval must be positive")
	}
	if inspection.State != StateUnpatched || !inspection.hasSelected {
		return nil, fmt.Errorf("payload is not in a uniquely known unpatched shape")
	}

	match := inspection.selected
	replacement, err := buildPatchedBytes(payload, match, intervalMS)
	if err != nil {
		return nil, err
	}

	out := make([]byte, len(payload)+len(replacement)-(match.end-match.start))
	copy(out, payload[:match.start])
	copy(out[match.start:], replacement)
	copy(out[match.start+len(replacement):], payload[match.end:])

	post := Inspect(out)
	if post.State != StatePatched || post.ShapeID != match.shapeID || post.IntervalMS != intervalMS {
		return nil, fmt.Errorf("post-patch validation failed: state=%s shape=%s interval=%d", post.State, post.ShapeID, post.IntervalMS)
	}
	return out, nil
}

func ExtractMatchedSnippet(payload []byte) ([]byte, Inspection, error) {
	inspection := Inspect(payload)
	if !inspection.hasSelected {
		return nil, inspection, fmt.Errorf("payload is not in a uniquely known shape")
	}
	match := inspection.selected
	return append([]byte(nil), payload[match.start:match.end]...), inspection, nil
}

func scanShapes(payload []byte) scanResult {
	var result scanResult
	for _, family := range shapeFamilies {
		if scanPattern(payload, family, PatchStateUnpatched, family.unpatched, &result) {
			return result
		}
		if scanPattern(payload, family, PatchStatePatched, family.patched, &result) {
			return result
		}
	}
	return result
}

func scanPattern(payload []byte, family shapeFamily, kind PatchState, pattern compiledPattern, result *scanResult) bool {
	searchStart := 0
	for searchStart < len(payload) {
		loc := pattern.re.FindSubmatchIndex(payload[searchStart:])
		if loc == nil {
			return false
		}

		match := regexMatch{
			pattern: pattern,
			idxs:    offsetIndices(loc, searchStart),
		}
		candidate, malformed := validateCandidate(payload, family, kind, match)
		if malformed {
			result.malformed = true
			return true
		}
		if candidate != nil {
			switch kind {
			case PatchStateUnpatched:
				result.unpatchedMatches++
			case PatchStatePatched:
				result.patchedMatches++
			}
			result.valid++
			if result.valid == 1 {
				result.first = *candidate
			} else {
				return true
			}
		}
		searchStart = match.idxs[0] + 1
	}
	return false
}

func offsetIndices(indices []int, offset int) []int {
	out := make([]int, len(indices))
	for i, index := range indices {
		if index < 0 {
			out[i] = -1
			continue
		}
		out[i] = index + offset
	}
	return out
}

func validateCandidate(payload []byte, family shapeFamily, kind PatchState, match regexMatch) (*shapeMatch, bool) {
	switch kind {
	case PatchStateUnpatched:
		if family.validateUnpatched != nil && !family.validateUnpatched(payload, match) {
			return nil, true
		}
		return &shapeMatch{
			kind:    kind,
			shapeID: family.id,
			start:   match.idxs[0],
			end:     match.idxs[1],
			match:   match,
		}, false
	case PatchStatePatched:
		if family.validatePatched != nil && !family.validatePatched(payload, match) {
			return nil, true
		}
		interval, ok := parsePositiveIntBytes(match.bytes(payload, "interval"))
		if !ok {
			return nil, true
		}
		return &shapeMatch{
			kind:     kind,
			shapeID:  family.id,
			start:    match.idxs[0],
			end:      match.idxs[1],
			match:    match,
			interval: interval,
		}, false
	default:
		return nil, false
	}
}

func validateUnpatchedMatchV1(payload []byte, match regexMatch) bool {
	return equalAllBytes(payload, match, "hooks", "hooksEffect") &&
		equalAllBytes(payload, match, "timer", "timerClear", "timerSet", "timerArg") &&
		equalAllBytes(payload, match, "clearArg", "clearArgRepeat") &&
		equalAllBytes(payload, match, "invoke", "invokeRepeat") &&
		equalAllBytes(payload, match, "refresh", "refreshDep") &&
		equalAllBytes(payload, match, "callback", "callbackInvoke", "callbackDep") &&
		equalAllBytes(payload, match, "state", "statePerm", "stateVim", "statePermAssign", "stateVimAssign") &&
		equalAllBytes(payload, match, "message", "messageDep") &&
		equalAllBytes(payload, match, "permission", "permissionAssign", "permissionDep") &&
		equalAllBytes(payload, match, "vim", "vimAssign", "vimDep")
}

func validatePatchedMatchV1(payload []byte, match regexMatch) bool {
	return equalAllBytes(payload, match, "hooks", "hooksCallback", "hooksEffect") &&
		equalAllBytes(payload, match, "intervalVar", "intervalVarClear") &&
		equalAllBytes(payload, match, "refresh", "refreshDep") &&
		equalAllBytes(payload, match, "callback", "callbackInvoke", "callbackDep") &&
		equalAllBytes(payload, match, "state", "statePerm", "stateVim", "statePermAssign", "stateVimAssign") &&
		equalAllBytes(payload, match, "message", "messageDep") &&
		equalAllBytes(payload, match, "permission", "permissionAssign", "permissionDep") &&
		equalAllBytes(payload, match, "vim", "vimAssign", "vimDep")
}

func validateUnpatchedMatchV2(payload []byte, match regexMatch) bool {
	return validateUnpatchedMatchV1(payload, match) &&
		equalAllBytes(payload, match, "state", "statePerm", "stateVim", "stateModel", "statePermAssign", "stateVimAssign", "stateModelAssign") &&
		equalAllBytes(payload, match, "model", "modelAssign", "modelDep")
}

func validatePatchedMatchV2(payload []byte, match regexMatch) bool {
	return validatePatchedMatchV1(payload, match) &&
		equalAllBytes(payload, match, "state", "statePerm", "stateVim", "stateModel", "statePermAssign", "stateVimAssign", "stateModelAssign") &&
		equalAllBytes(payload, match, "model", "modelAssign", "modelDep")
}

func equalAllBytes(payload []byte, match regexMatch, names ...string) bool {
	if len(names) == 0 {
		return true
	}
	first := match.bytes(payload, names[0])
	if len(first) == 0 {
		return false
	}
	for _, name := range names[1:] {
		if !bytes.Equal(first, match.bytes(payload, name)) {
			return false
		}
	}
	return true
}

func parsePositiveIntBytes(data []byte) (int, bool) {
	if len(data) == 0 {
		return 0, false
	}
	maxInt := int(^uint(0) >> 1)
	value := 0
	for _, digit := range data {
		if digit < '0' || digit > '9' {
			return 0, false
		}
		if value > (maxInt-int(digit-'0'))/10 {
			return 0, false
		}
		value = value*10 + int(digit-'0')
		if value <= 0 {
			return 0, false
		}
	}
	return value, true
}

func buildPatchedBytes(payload []byte, match shapeMatch, intervalMS int) ([]byte, error) {
	if intervalMS <= 0 {
		return nil, fmt.Errorf("interval must be positive")
	}

	for _, family := range shapeFamilies {
		if family.id == match.shapeID {
			if family.buildReplacement == nil {
				break
			}
			return family.buildReplacement(payload, match, intervalMS)
		}
	}
	return nil, fmt.Errorf("unsupported shape family %q", match.shapeID)
}

func compilePattern(expr string) compiledPattern {
	re := regexp.MustCompile(expr)
	groupIndex := make(map[string]int)
	for i, name := range re.SubexpNames() {
		if name != "" {
			groupIndex[name] = i
		}
	}
	return compiledPattern{re: re, groupIndex: groupIndex}
}

func newStatuslineDebounceV1() shapeFamily {
	id := identifierPattern
	unpatchedPattern := fmt.Sprintf(
		`,(?P<callback>%[1]s)=(?P<hooks>%[1]s)\.useCallback\(\(\)=>\{if\((?P<timer>%[1]s)\.current!==void 0\)clearTimeout\((?P<timerClear>%[1]s)\.current\);(?P<timerSet>%[1]s)\.current=setTimeout\(\((?P<clearArg>%[1]s),(?P<invoke>%[1]s)\)=>\{(?P<clearArgRepeat>%[1]s)\.current=void 0,(?P<invokeRepeat>%[1]s)\(\)\},300,(?P<timerArg>%[1]s),(?P<refresh>%[1]s)\)\},\[(?P<refreshDep>%[1]s)\]\);(?P<hooksEffect>%[1]s)\.useEffect\(\(\)=>\{if\((?P<message>%[1]s)!==(?P<state>%[1]s)\.current\.messageId\|\|(?P<permission>%[1]s)!==(?P<statePerm>%[1]s)\.current\.permissionMode\|\|(?P<vim>%[1]s)!==(?P<stateVim>%[1]s)\.current\.vimMode\)(?P<statePermAssign>%[1]s)\.current\.permissionMode=(?P<permissionAssign>%[1]s),(?P<stateVimAssign>%[1]s)\.current\.vimMode=(?P<vimAssign>%[1]s),(?P<callbackInvoke>%[1]s)\(\)\},\[(?P<messageDep>%[1]s),(?P<permissionDep>%[1]s),(?P<vimDep>%[1]s),(?P<callbackDep>%[1]s)\]\);`,
		id,
	)
	patchedPattern := fmt.Sprintf(
		`,(?P<effectVar>%[1]s)=(?P<hooks>%[1]s)\.useEffect\(\(\)=>\{const (?P<intervalVar>%[1]s)=setInterval\(\(\)=>(?P<refresh>%[1]s)\(\),(?P<interval>[1-9][0-9]*)\);return\(\)=>clearInterval\((?P<intervalVarClear>%[1]s)\);\},\[(?P<refreshDep>%[1]s)\]\),(?P<callback>%[1]s)=(?P<hooksCallback>%[1]s)\.useCallback\(\(\)=>\{\},\[\]\);(?P<hooksEffect>%[1]s)\.useEffect\(\(\)=>\{if\((?P<message>%[1]s)!==(?P<state>%[1]s)\.current\.messageId\|\|(?P<permission>%[1]s)!==(?P<statePerm>%[1]s)\.current\.permissionMode\|\|(?P<vim>%[1]s)!==(?P<stateVim>%[1]s)\.current\.vimMode\)(?P<statePermAssign>%[1]s)\.current\.permissionMode=(?P<permissionAssign>%[1]s),(?P<stateVimAssign>%[1]s)\.current\.vimMode=(?P<vimAssign>%[1]s),(?P<callbackInvoke>%[1]s)\(\)\},\[(?P<messageDep>%[1]s),(?P<permissionDep>%[1]s),(?P<vimDep>%[1]s),(?P<callbackDep>%[1]s)\]\);`,
		id,
	)
	return shapeFamily{
		id:                ShapeIDStatuslineDebounceV1,
		observedVersions:  []string{"2.1.84", "2.1.85"},
		unpatched:         compilePattern(unpatchedPattern),
		patched:           compilePattern(patchedPattern),
		validateUnpatched: validateUnpatchedMatchV1,
		validatePatched:   validatePatchedMatchV1,
		buildReplacement:  buildPatchedBytesV1,
	}
}

func newStatuslineDebounceV2() shapeFamily {
	id := identifierPattern
	unpatchedPattern := fmt.Sprintf(
		`,(?P<callback>%[1]s)=(?P<hooks>%[1]s)\.useCallback\(\(\)=>\{if\((?P<timer>%[1]s)\.current!==void 0\)clearTimeout\((?P<timerClear>%[1]s)\.current\);(?P<timerSet>%[1]s)\.current=setTimeout\(\((?P<clearArg>%[1]s),(?P<invoke>%[1]s)\)=>\{(?P<clearArgRepeat>%[1]s)\.current=void 0,(?P<invokeRepeat>%[1]s)\(\)\},300,(?P<timerArg>%[1]s),(?P<refresh>%[1]s)\)\},\[(?P<refreshDep>%[1]s)\]\);(?P<hooksEffect>%[1]s)\.useEffect\(\(\)=>\{if\((?P<message>%[1]s)!==(?P<state>%[1]s)\.current\.messageId\|\|(?P<permission>%[1]s)!==(?P<statePerm>%[1]s)\.current\.permissionMode\|\|(?P<vim>%[1]s)!==(?P<stateVim>%[1]s)\.current\.vimMode\|\|(?P<model>%[1]s)!==(?P<stateModel>%[1]s)\.current\.mainLoopModel\)(?P<statePermAssign>%[1]s)\.current\.permissionMode=(?P<permissionAssign>%[1]s),(?P<stateVimAssign>%[1]s)\.current\.vimMode=(?P<vimAssign>%[1]s),(?P<stateModelAssign>%[1]s)\.current\.mainLoopModel=(?P<modelAssign>%[1]s),(?P<callbackInvoke>%[1]s)\(\)\},\[(?P<messageDep>%[1]s),(?P<permissionDep>%[1]s),(?P<vimDep>%[1]s),(?P<modelDep>%[1]s),(?P<callbackDep>%[1]s)\]\);`,
		id,
	)
	patchedPattern := fmt.Sprintf(
		`,(?P<effectVar>%[1]s)=(?P<hooks>%[1]s)\.useEffect\(\(\)=>\{const (?P<intervalVar>%[1]s)=setInterval\(\(\)=>(?P<refresh>%[1]s)\(\),(?P<interval>[1-9][0-9]*)\);return\(\)=>clearInterval\((?P<intervalVarClear>%[1]s)\);\},\[(?P<refreshDep>%[1]s)\]\),(?P<callback>%[1]s)=(?P<hooksCallback>%[1]s)\.useCallback\(\(\)=>\{\},\[\]\);(?P<hooksEffect>%[1]s)\.useEffect\(\(\)=>\{if\((?P<message>%[1]s)!==(?P<state>%[1]s)\.current\.messageId\|\|(?P<permission>%[1]s)!==(?P<statePerm>%[1]s)\.current\.permissionMode\|\|(?P<vim>%[1]s)!==(?P<stateVim>%[1]s)\.current\.vimMode\|\|(?P<model>%[1]s)!==(?P<stateModel>%[1]s)\.current\.mainLoopModel\)(?P<statePermAssign>%[1]s)\.current\.permissionMode=(?P<permissionAssign>%[1]s),(?P<stateVimAssign>%[1]s)\.current\.vimMode=(?P<vimAssign>%[1]s),(?P<stateModelAssign>%[1]s)\.current\.mainLoopModel=(?P<modelAssign>%[1]s),(?P<callbackInvoke>%[1]s)\(\)\},\[(?P<messageDep>%[1]s),(?P<permissionDep>%[1]s),(?P<vimDep>%[1]s),(?P<modelDep>%[1]s),(?P<callbackDep>%[1]s)\]\);`,
		id,
	)
	return shapeFamily{
		id:                ShapeIDStatuslineDebounceV2,
		observedVersions:  []string{"2.1.86"},
		unpatched:         compilePattern(unpatchedPattern),
		patched:           compilePattern(patchedPattern),
		validateUnpatched: validateUnpatchedMatchV2,
		validatePatched:   validatePatchedMatchV2,
		buildReplacement:  buildPatchedBytesV2,
	}
}

func observedVersionsForShape(shapeID string) []string {
	for _, family := range shapeFamilies {
		if family.id == shapeID {
			return append([]string(nil), family.observedVersions...)
		}
	}
	return nil
}

func sanitizeFieldValue(value string) string {
	replacer := strings.NewReplacer(
		"\r", `\r`,
		"\n", `\n`,
	)
	return replacer.Replace(value)
}

type trackedField struct {
	value    string
	property string
	assign   bool
}

func buildPatchedBytesV1(payload []byte, match shapeMatch, intervalMS int) ([]byte, error) {
	return buildPatchedReplacement(
		match.match.string(payload, "hooks"),
		match.match.string(payload, "refresh"),
		match.match.string(payload, "callback"),
		match.match.string(payload, "state"),
		[]trackedField{
			{value: match.match.string(payload, "message"), property: "messageId", assign: false},
			{value: match.match.string(payload, "permission"), property: "permissionMode", assign: true},
			{value: match.match.string(payload, "vim"), property: "vimMode", assign: true},
		},
		intervalMS,
	), nil
}

func buildPatchedBytesV2(payload []byte, match shapeMatch, intervalMS int) ([]byte, error) {
	return buildPatchedReplacement(
		match.match.string(payload, "hooks"),
		match.match.string(payload, "refresh"),
		match.match.string(payload, "callback"),
		match.match.string(payload, "state"),
		[]trackedField{
			{value: match.match.string(payload, "message"), property: "messageId", assign: false},
			{value: match.match.string(payload, "permission"), property: "permissionMode", assign: true},
			{value: match.match.string(payload, "vim"), property: "vimMode", assign: true},
			{value: match.match.string(payload, "model"), property: "mainLoopModel", assign: true},
		},
		intervalMS,
	), nil
}

func buildPatchedReplacement(hooks, refresh, callback, state string, fields []trackedField, intervalMS int) []byte {
	base := bytes.NewBuffer(nil)
	base.WriteByte(',')
	base.WriteString("unused1=")
	base.WriteString(hooks)
	base.WriteString(".useEffect(()=>{const id=setInterval(()=>")
	base.WriteString(refresh)
	base.WriteString("(),")
	base.WriteString(strconv.Itoa(intervalMS))
	base.WriteString(");return()=>clearInterval(id);},[")
	base.WriteString(refresh)
	base.WriteString("]),")
	base.WriteString(callback)
	base.WriteString("=")
	base.WriteString(hooks)
	base.WriteString(".useCallback(()=>{},[]);")
	base.WriteString(hooks)
	base.WriteString(".useEffect(()=>{if(")
	for i, field := range fields {
		if i > 0 {
			base.WriteString("||")
		}
		base.WriteString(field.value)
		base.WriteString("!==")
		base.WriteString(state)
		base.WriteString(".current.")
		base.WriteString(field.property)
	}
	base.WriteString(")")
	for _, field := range fields {
		if !field.assign {
			continue
		}
		base.WriteString(state)
		base.WriteString(".current.")
		base.WriteString(field.property)
		base.WriteString("=")
		base.WriteString(field.value)
		base.WriteString(",")
	}
	base.WriteString(callback)
	base.WriteString("()},[")
	for i, field := range fields {
		if i > 0 {
			base.WriteString(",")
		}
		base.WriteString(field.value)
	}
	base.WriteString(",")
	base.WriteString(callback)
	base.WriteString("]);")
	return base.Bytes()
}

func (m regexMatch) bytes(payload []byte, name string) []byte {
	index, ok := m.pattern.groupIndex[name]
	if !ok {
		return nil
	}
	start := m.idxs[index*2]
	end := m.idxs[index*2+1]
	if start < 0 || end < 0 {
		return nil
	}
	return payload[start:end]
}

func (m regexMatch) string(payload []byte, name string) string {
	return string(m.bytes(payload, name))
}

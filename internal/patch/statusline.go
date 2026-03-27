package patch

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
)

var versionPattern = regexp.MustCompile(`VERSION:"([^"]+)"`)

const (
	ShapeIDStatuslineDebounceV1 = "statusline_debounce_v1"
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
	IntervalMS       int
	LiveVerified     bool
	UnpatchedMatches int
	PatchedMatches   int
}

type shapeFamily struct {
	id        string
	unpatched *regexp.Regexp
	patched   *regexp.Regexp
}

type shapeMatch struct {
	kind     PatchState
	shapeID  string
	start    int
	end      int
	groups   map[string]string
	interval int
}

var (
	identifierPattern = `[A-Za-z_$][A-Za-z0-9_$]*`
	shapeFamilies     = []shapeFamily{newStatuslineDebounceV1()}
	liveVerified      = map[string]struct{}{
		"2.1.84": {},
		"2.1.85": {},
	}
)

func Inspect(payload []byte) Inspection {
	version := DetectVersion(payload)
	matches := collectMatches(payload)
	unpatchedMatches := 0
	patchedMatches := 0
	for _, match := range matches {
		switch match.kind {
		case PatchStateUnpatched:
			unpatchedMatches++
		case PatchStatePatched:
			patchedMatches++
		}
	}

	inspection := Inspection{
		Version:          version,
		LiveVerified:     IsLiveVerifiedVersion(version),
		UnpatchedMatches: unpatchedMatches,
		PatchedMatches:   patchedMatches,
		PatchState:       PatchStateUnknown,
	}

	switch len(matches) {
	case 0:
		inspection.State = StateUnrecognizedShape
		inspection.ShapeState = ShapeStateUnrecognized
		return inspection
	case 1:
		match := matches[0]
		inspection.ShapeID = match.shapeID
		inspection.ShapeState = ShapeStateKnown
		inspection.PatchState = match.kind
		switch match.kind {
		case PatchStateUnpatched:
			inspection.State = StateUnpatched
		case PatchStatePatched:
			inspection.State = StatePatched
			inspection.IntervalMS = match.interval
		default:
			inspection.State = StateUnrecognizedShape
			inspection.ShapeState = ShapeStateUnrecognized
			inspection.PatchState = PatchStateUnknown
		}
		return inspection
	default:
		inspection.State = StateAmbiguousShape
		inspection.ShapeState = ShapeStateAmbiguous
		return inspection
	}
}

func DetectVersion(payload []byte) string {
	match := versionPattern.FindSubmatch(payload)
	if len(match) != 2 {
		return ""
	}
	return string(match[1])
}

func IsLiveVerifiedVersion(version string) bool {
	_, ok := liveVerified[version]
	return ok
}

func Apply(payload []byte, intervalMS int) ([]byte, error) {
	inspection := Inspect(payload)
	switch inspection.State {
	case StateUnpatched:
		return ApplyKnownUnpatched(payload, intervalMS)
	case StatePatched:
		return nil, fmt.Errorf("payload already patched at %dms", inspection.IntervalMS)
	case StateAmbiguousShape:
		return nil, fmt.Errorf("patch match is ambiguous")
	default:
		return nil, fmt.Errorf("payload shape is unrecognized for patching")
	}
}

func ApplyKnownUnpatched(payload []byte, intervalMS int) ([]byte, error) {
	if intervalMS <= 0 {
		return nil, fmt.Errorf("interval must be positive")
	}

	matches := collectMatches(payload)
	if len(matches) != 1 || matches[0].kind != PatchStateUnpatched {
		return nil, fmt.Errorf("payload is not in a uniquely known unpatched shape")
	}

	match := matches[0]
	replacement, err := buildPatchedBytes(match, intervalMS)
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

func collectMatches(payload []byte) []shapeMatch {
	var matches []shapeMatch
	for _, family := range shapeFamilies {
		matches = append(matches, findUnpatchedMatches(family, payload)...)
		matches = append(matches, findPatchedMatches(family, payload)...)
	}
	return matches
}

func findUnpatchedMatches(family shapeFamily, payload []byte) []shapeMatch {
	var matches []shapeMatch
	indexes := family.unpatched.FindAllSubmatchIndex(payload, -1)
	names := family.unpatched.SubexpNames()
	for _, idxs := range indexes {
		groups := extractGroups(payload, names, idxs)
		if !validateUnpatchedGroups(groups) {
			continue
		}
		matches = append(matches, shapeMatch{
			kind:    PatchStateUnpatched,
			shapeID: family.id,
			start:   idxs[0],
			end:     idxs[1],
			groups:  groups,
		})
	}
	return matches
}

func findPatchedMatches(family shapeFamily, payload []byte) []shapeMatch {
	var matches []shapeMatch
	indexes := family.patched.FindAllSubmatchIndex(payload, -1)
	names := family.patched.SubexpNames()
	for _, idxs := range indexes {
		groups := extractGroups(payload, names, idxs)
		if !validatePatchedGroups(groups) {
			continue
		}
		interval, err := strconv.Atoi(groups["interval"])
		if err != nil || interval <= 0 {
			continue
		}
		matches = append(matches, shapeMatch{
			kind:     PatchStatePatched,
			shapeID:  family.id,
			start:    idxs[0],
			end:      idxs[1],
			groups:   groups,
			interval: interval,
		})
	}
	return matches
}

func extractGroups(payload []byte, names []string, idxs []int) map[string]string {
	groups := make(map[string]string, len(names))
	for i := 1; i < len(names); i++ {
		name := names[i]
		if name == "" {
			continue
		}
		start := idxs[i*2]
		end := idxs[i*2+1]
		if start < 0 || end < 0 {
			continue
		}
		groups[name] = string(payload[start:end])
	}
	return groups
}

func validateUnpatchedGroups(groups map[string]string) bool {
	return equalAll(groups, "hooks", "hooksEffect") &&
		equalAll(groups, "timer", "timerClear", "timerSet", "timerArg") &&
		equalAll(groups, "clearArg", "clearArgRepeat") &&
		equalAll(groups, "invoke", "invokeRepeat") &&
		equalAll(groups, "refresh", "refreshDep") &&
		equalAll(groups, "callback", "callbackInvoke", "callbackDep") &&
		equalAll(groups, "state", "statePerm", "stateVim", "statePermAssign", "stateVimAssign") &&
		equalAll(groups, "message", "messageDep") &&
		equalAll(groups, "permission", "permissionAssign", "permissionDep") &&
		equalAll(groups, "vim", "vimAssign", "vimDep")
}

func validatePatchedGroups(groups map[string]string) bool {
	return equalAll(groups, "hooks", "hooksCallback", "hooksEffect") &&
		equalAll(groups, "intervalVar", "intervalVarClear") &&
		equalAll(groups, "refresh", "refreshDep") &&
		equalAll(groups, "callback", "callbackInvoke", "callbackDep") &&
		equalAll(groups, "state", "statePerm", "stateVim", "statePermAssign", "stateVimAssign") &&
		equalAll(groups, "message", "messageDep") &&
		equalAll(groups, "permission", "permissionAssign", "permissionDep") &&
		equalAll(groups, "vim", "vimAssign", "vimDep")
}

func equalAll(groups map[string]string, keys ...string) bool {
	if len(keys) == 0 {
		return true
	}
	value, ok := groups[keys[0]]
	if !ok || value == "" {
		return false
	}
	for _, key := range keys[1:] {
		if groups[key] != value {
			return false
		}
	}
	return true
}

func buildPatchedBytes(match shapeMatch, intervalMS int) ([]byte, error) {
	if intervalMS <= 0 {
		return nil, fmt.Errorf("interval must be positive")
	}

	hooks := match.groups["hooks"]
	refresh := match.groups["refresh"]
	callback := match.groups["callback"]
	message := match.groups["message"]
	state := match.groups["state"]
	permission := match.groups["permission"]
	vim := match.groups["vim"]

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
	base.WriteString(message)
	base.WriteString("!==")
	base.WriteString(state)
	base.WriteString(".current.messageId||")
	base.WriteString(permission)
	base.WriteString("!==")
	base.WriteString(state)
	base.WriteString(".current.permissionMode||")
	base.WriteString(vim)
	base.WriteString("!==")
	base.WriteString(state)
	base.WriteString(".current.vimMode)")
	base.WriteString(state)
	base.WriteString(".current.permissionMode=")
	base.WriteString(permission)
	base.WriteString(",")
	base.WriteString(state)
	base.WriteString(".current.vimMode=")
	base.WriteString(vim)
	base.WriteString(",")
	base.WriteString(callback)
	base.WriteString("()},[")
	base.WriteString(message)
	base.WriteString(",")
	base.WriteString(permission)
	base.WriteString(",")
	base.WriteString(vim)
	base.WriteString(",")
	base.WriteString(callback)
	base.WriteString("]);")
	return base.Bytes(), nil
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
		id:        ShapeIDStatuslineDebounceV1,
		unpatched: regexp.MustCompile(unpatchedPattern),
		patched:   regexp.MustCompile(patchedPattern),
	}
}

package patch

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
)

const SupportedVersion = "2.1.84"

var (
	versionPattern = regexp.MustCompile(`VERSION:"([^"]+)"`)
	unpatchedBytes = []byte(`,G=dY.useCallback(()=>{if(j.current!==void 0)clearTimeout(j.current);j.current=setTimeout((k,N)=>{k.current=void 0,N()},300,j,J)},[J]);dY.useEffect(()=>{if($!==Y.current.messageId||D!==Y.current.permissionMode||A!==Y.current.vimMode)Y.current.permissionMode=D,Y.current.vimMode=A,G()},[$,D,A,G]);`)
	patchedPrefix  = []byte(`,unused1=dY.useEffect(()=>{const id=setInterval(()=>J(),`)
	patchedSuffix  = []byte(`);return()=>clearInterval(id);},[J]),G=dY.useCallback(()=>{},[]);dY.useEffect(()=>{if($!==Y.current.messageId||D!==Y.current.permissionMode||A!==Y.current.vimMode)Y.current.permissionMode=D,Y.current.vimMode=A,G()},[$,D,A,G]);`)
)

type State string

const (
	StateUnpatched   State = "unpatched"
	StatePatched     State = "patched"
	StateUnsupported State = "unsupported"
	StateAmbiguous   State = "ambiguous"
)

type Inspection struct {
	State            State
	Version          string
	IntervalMS       int
	UnpatchedMatches int
	PatchedMatches   int
}

func Inspect(payload []byte) Inspection {
	version := DetectVersion(payload)
	unpatchedMatches := bytes.Count(payload, unpatchedBytes)
	patchedIntervals := findPatchedIntervals(payload)

	inspection := Inspection{
		Version:          version,
		UnpatchedMatches: unpatchedMatches,
		PatchedMatches:   len(patchedIntervals),
	}

	if unpatchedMatches > 1 || len(patchedIntervals) > 1 || (unpatchedMatches == 1 && len(patchedIntervals) == 1) {
		inspection.State = StateAmbiguous
		return inspection
	}

	if version != SupportedVersion {
		inspection.State = StateUnsupported
		if len(patchedIntervals) == 1 {
			inspection.IntervalMS = patchedIntervals[0]
		}
		return inspection
	}

	switch {
	case unpatchedMatches == 1:
		inspection.State = StateUnpatched
	case len(patchedIntervals) == 1:
		inspection.State = StatePatched
		inspection.IntervalMS = patchedIntervals[0]
	default:
		inspection.State = StateUnsupported
	}

	return inspection
}

func DetectVersion(payload []byte) string {
	match := versionPattern.FindSubmatch(payload)
	if len(match) != 2 {
		return ""
	}
	return string(match[1])
}

func Apply(payload []byte, intervalMS int) ([]byte, error) {
	inspection := Inspect(payload)
	switch inspection.State {
	case StateUnpatched:
		// continue
	case StatePatched:
		return nil, fmt.Errorf("payload already patched at %dms", inspection.IntervalMS)
	case StateAmbiguous:
		return nil, fmt.Errorf("patch match is ambiguous")
	default:
		return nil, fmt.Errorf("payload is unsupported for patching")
	}

	replacement, err := buildPatchedBytes(intervalMS)
	if err != nil {
		return nil, err
	}

	out := append([]byte(nil), payload...)
	index := bytes.Index(out, unpatchedBytes)
	if index < 0 {
		return nil, fmt.Errorf("unpatched signature not found")
	}
	out = append(out[:index], append(replacement, out[index+len(unpatchedBytes):]...)...)

	post := Inspect(out)
	if post.State != StatePatched || post.IntervalMS != intervalMS {
		return nil, fmt.Errorf("post-patch validation failed: state=%s interval=%d", post.State, post.IntervalMS)
	}
	return out, nil
}

func buildPatchedBytes(intervalMS int) ([]byte, error) {
	if intervalMS <= 0 {
		return nil, fmt.Errorf("interval must be positive")
	}

	base := append([]byte(nil), patchedPrefix...)
	base = append(base, []byte(strconv.Itoa(intervalMS))...)
	base = append(base, patchedSuffix...)
	return base, nil
}

func findPatchedIntervals(payload []byte) []int {
	var intervals []int
	searchStart := 0
	for {
		offset := bytes.Index(payload[searchStart:], patchedPrefix)
		if offset < 0 {
			return intervals
		}
		offset += searchStart

		numberStart := offset + len(patchedPrefix)
		numberEnd := numberStart
		for numberEnd < len(payload) && payload[numberEnd] >= '0' && payload[numberEnd] <= '9' {
			numberEnd++
		}
		if numberEnd == numberStart {
			searchStart = offset + 1
			continue
		}
		if !bytes.HasPrefix(payload[numberEnd:], patchedSuffix) {
			searchStart = offset + 1
			continue
		}
		interval, err := strconv.Atoi(string(payload[numberStart:numberEnd]))
		if err == nil {
			intervals = append(intervals, interval)
		}
		searchStart = numberEnd + len(patchedSuffix)
	}
}

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"

	"github.com/leonardkore/claude-statusline-patch/internal/bun"
	"github.com/leonardkore/claude-statusline-patch/internal/fileutil"
	"github.com/leonardkore/claude-statusline-patch/internal/patch"
)

const maxBinarySizeBytes int64 = 1 << 30

func main() {
	binaryPath := flag.String("binary", "", "path to the Claude binary")
	outputPath := flag.String("output", "", "optional output path for the extracted snippet")
	flag.Parse()

	if *binaryPath == "" {
		fmt.Fprintln(os.Stderr, "binary path is required")
		os.Exit(2)
	}

	data, err := fileutil.ReadBoundedRegularFile(*binaryPath, "target binary", maxBinarySizeBytes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read binary: %v\n", err)
		os.Exit(1)
	}

	bundle, err := bun.Extract(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "extract embedded Bun payload: %v\n", err)
		os.Exit(1)
	}
	graph, err := bun.ParseModuleGraph(bundle.Metadata.Format, bundle.Payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse embedded Bun module graph: %v\n", err)
		os.Exit(1)
	}
	_, entryModule, err := graph.EntryPointModule()
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve entry module: %v\n", err)
		os.Exit(1)
	}
	entryContents, err := graph.Slice(entryModule.Contents)
	if err != nil {
		fmt.Fprintf(os.Stderr, "slice entry module: %v\n", err)
		os.Exit(1)
	}

	snippet, inspection, err := patch.ExtractMatchedSnippet(entryContents)
	if err != nil {
		fmt.Fprintf(os.Stderr, "extract matched statusline snippet: %v\n", err)
		os.Exit(1)
	}

	if *outputPath != "" {
		if err := os.WriteFile(*outputPath, snippet, 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "write output: %v\n", err)
			os.Exit(1)
		}
	} else if _, err := os.Stdout.Write(snippet); err != nil {
		fmt.Fprintf(os.Stderr, "write stdout: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "\nversion=%s shape_id=%s state=%s patch_state=%s sha256=%s\n",
		inspection.Version,
		inspection.ShapeID,
		inspection.State,
		inspection.PatchState,
		sha256Hex(data),
	)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

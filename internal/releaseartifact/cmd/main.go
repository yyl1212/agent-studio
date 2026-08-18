package main

import (
	"fmt"
	"os"

	"github.com/yyl1212/agent-studio/internal/releaseartifact"
)

const usage = "usage: check-release-artifacts collection <dist> <version> | target <dist> <version> <goos> <goarch> <commit>"

func main() {
	var (
		err     error
		success string
	)
	switch {
	case len(os.Args) == 4 && os.Args[1] == "collection":
		err = releaseartifact.VerifyCollection(releaseartifact.Config{
			DistDir: os.Args[2],
			Version: os.Args[3],
		})
		success = fmt.Sprintf("release artifact collection ok: %s", os.Args[3])
	case len(os.Args) == 7 && os.Args[1] == "target":
		err = releaseartifact.VerifyTarget(releaseartifact.Config{
			DistDir: os.Args[2],
			Version: os.Args[3],
			GOOS:    os.Args[4],
			GOARCH:  os.Args[5],
			Commit:  os.Args[6],
		})
		success = fmt.Sprintf("release artifact target ok: %s %s/%s", os.Args[3], os.Args[4], os.Args[5])
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "verify release artifacts: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(success)
}

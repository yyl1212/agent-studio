package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/yyl1212/agent-studio/internal/buildinfo"
	"github.com/yyl1212/agent-studio/internal/scaffold"
)

const helpText = "backup create\nbackup inspect\nbackup restore\ndoctor\ngenerate\nnode index refresh\nnode index status\nnode info\nnode init\nnode inspect\nnode package init\nnode search\nnode test\nversion\n"

type appDependencies struct {
	backup          func(context.Context, []string, io.Writer, io.Writer) int
	workingDir      func() (string, error)
	buildInfo       func() buildinfo.Info
	diagnose        func(context.Context, string) []CheckResult
	generate        func(context.Context, string) (generateResult, error)
	nodeInit        func(context.Context, string, string) (nodeInitResult, error)
	nodeIndex       func(context.Context, []string, io.Writer, io.Writer) int
	nodeInspect     func(context.Context, string, string, bool, io.Writer, io.Writer) int
	nodePackageInit func(context.Context, string, packageInitInput) error
	nodeSearch      func(context.Context, []string, io.Writer, io.Writer) int
	nodeTest        func(context.Context, string, string, io.Writer, io.Writer) int
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return run(ctx, args, stdout, stderr, appDependencies{
		backup:     backupCommand,
		workingDir: os.Getwd,
		buildInfo:  buildinfo.Current,
		diagnose: func(ctx context.Context, root string) []CheckResult {
			return Diagnose(ctx, root, defaultDoctorDeps(root))
		},
		generate:        generateNodes,
		nodeInit:        initializeNode,
		nodeIndex:       nodeIndexCommand,
		nodeInspect:     inspectNodePackage,
		nodePackageInit: initializeNodePackage,
		nodeSearch:      nodeSearchCommand,
		nodeTest:        testNodePackage,
	})
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, dependencies appDependencies) int {
	if len(args) == 0 {
		_, _ = io.WriteString(stdout, helpText)
		return 0
	}
	if args[0] == "help" {
		if len(args) != 1 {
			_, _ = io.WriteString(stderr, "help takes no arguments\n")
			return 2
		}
		_, _ = io.WriteString(stdout, helpText)
		return 0
	}

	switch args[0] {
	case "backup":
		return dependencies.backup(ctx, args[1:], stdout, stderr)
	case "version":
		if len(args) != 1 {
			_, _ = io.WriteString(stderr, "version takes no arguments\n")
			return 2
		}
		info := dependencies.buildInfo()
		_, _ = fmt.Fprintf(
			stdout,
			"agent-studio %s (sdk %s; api %s; commit %s; dirty %t)\n",
			info.Version,
			info.SDKVersion,
			info.APIVersion,
			info.Revision,
			info.Dirty,
		)
		return 0
	case "doctor":
		if len(args) != 1 {
			_, _ = io.WriteString(stderr, "doctor takes no arguments\n")
			return 2
		}
		root, err := dependencies.workingDir()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "determine working directory: %v\n", err)
			return 1
		}
		failed := false
		for _, result := range dependencies.diagnose(ctx, root) {
			_, _ = fmt.Fprintf(stdout, "[%s] %s: %s\n", result.Status, result.Name, result.Detail)
			failed = failed || result.Status == checkFail
		}
		if failed {
			return 1
		}
		return 0
	case "generate":
		if len(args) != 1 {
			_, _ = io.WriteString(stderr, "generate takes no arguments\n")
			return 2
		}
		start, err := dependencies.workingDir()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "determine working directory: %v\n", err)
			return 1
		}
		result, err := dependencies.generate(ctx, start)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "generate nodes: %v\n", err)
			return 1
		}
		status := "unchanged"
		if result.Changed {
			status = "generated"
		}
		_, _ = fmt.Fprintf(stdout, "%s %s\n", status, result.Path)
		return 0
	case "node":
		if len(args) < 2 {
			_, _ = io.WriteString(stderr, "node requires index, info, init, inspect, package, search, or test\n")
			return 2
		}
		switch args[1] {
		case "index":
			return dependencies.nodeIndex(ctx, args[2:], stdout, stderr)
		case "info", "search":
			return dependencies.nodeSearch(ctx, args[1:], stdout, stderr)
		case "init":
			if len(args) != 3 {
				_, _ = io.WriteString(stderr, "node init requires exactly one name\n")
				return 2
			}
			if err := scaffold.ValidateName(args[2]); err != nil {
				_, _ = fmt.Fprintf(stderr, "node init: %v\n", err)
				return 2
			}
			start, err := dependencies.workingDir()
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "determine working directory: %v\n", err)
				return 1
			}
			result, err := dependencies.nodeInit(ctx, start, args[2])
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "initialize node: %v\n", err)
				return 1
			}
			_, _ = fmt.Fprintf(stdout, "created %s\n", result.Directory)
			_, _ = fmt.Fprintf(stdout, "next: CGO_ENABLED=0 go run ./cmd/agent-studio node test ./%s\n", result.Directory)
			_, _ = io.WriteString(stdout, "next: CGO_ENABLED=0 go run ./cmd/agent-studio generate\n")
			return 0
		case "test":
			if len(args) != 3 {
				_, _ = io.WriteString(stderr, "node test requires exactly one package\n")
				return 2
			}
			start, err := dependencies.workingDir()
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "determine working directory: %v\n", err)
				return 1
			}
			return dependencies.nodeTest(ctx, start, args[2], stdout, stderr)
		case "inspect":
			jsonOutput := false
			importPath := ""
			switch {
			case len(args) == 3 && !strings.HasPrefix(args[2], "-"):
				importPath = args[2]
			case len(args) == 4 && args[2] == "--json" && !strings.HasPrefix(args[3], "-"):
				jsonOutput = true
				importPath = args[3]
			default:
				_, _ = io.WriteString(stderr, "node inspect usage: node inspect [--json] <import-path>\n")
				return 2
			}
			start, err := dependencies.workingDir()
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "determine working directory: %v\n", err)
				return 1
			}
			return dependencies.nodeInspect(ctx, start, importPath, jsonOutput, stdout, stderr)
		case "package":
			if len(args) < 3 || args[2] != "init" {
				_, _ = io.WriteString(stderr, "node package requires init\n")
				return 2
			}
			flags := flag.NewFlagSet("node package init", flag.ContinueOnError)
			flags.SetOutput(stderr)
			input := packageInitInput{}
			flags.StringVar(&input.DisplayName, "display-name", "", "package display name")
			flags.StringVar(&input.Description, "description", "", "package description")
			flags.StringVar(&input.License, "license", "", "package license")
			flags.StringVar(&input.Repository, "repository", "", "package repository")
			flags.StringVar(&input.RuntimeMin, "runtime-min", "", "minimum runtime version")
			flags.StringVar(&input.RuntimeMaxExclusive, "runtime-max-exclusive", "", "exclusive maximum runtime version")
			if err := flags.Parse(args[3:]); err != nil {
				return 2
			}
			if flags.NArg() != 0 {
				_, _ = io.WriteString(stderr, "node package init takes flags only\n")
				return 2
			}
			missing := missingPackageInitFlags(input)
			if len(missing) != 0 {
				_, _ = fmt.Fprintf(stderr, "node package init required flags: %s\n", strings.Join(missing, ", "))
				return 2
			}
			start, err := dependencies.workingDir()
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "determine working directory: %v\n", err)
				return 1
			}
			if err := dependencies.nodePackageInit(ctx, start, input); err != nil {
				_, _ = fmt.Fprintf(stderr, "initialize node package: %v\n", err)
				return 1
			}
			_, _ = io.WriteString(stdout, "created agent-studio.node-package.json\n")
			return 0
		default:
			_, _ = fmt.Fprintf(stderr, "unknown node command %q\n", args[1])
			return 2
		}
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return 2
	}
}

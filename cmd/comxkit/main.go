package main

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/huynhanx03/go-common/internal/kit"
)

const commonModulePath = "github.com/huynhanx03/go-common"

func main() {
	version, commonVersion := buildVersions()
	command := kit.NewCommand(kit.Options{
		In:            os.Stdin,
		Out:           os.Stdout,
		Err:           os.Stderr,
		WorkDir:       ".",
		Version:       version,
		CommonVersion: commonVersion,
		Interactive:   interactiveTerminal(os.Stdin, os.Stderr),
	})
	if err := command.ExecuteContext(context.Background()); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func interactiveTerminal(input, output *os.File) bool {
	inputInfo, err := input.Stat()
	if err != nil || inputInfo.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	outputInfo, err := output.Stat()
	return err == nil && outputInfo.Mode()&os.ModeCharDevice != 0
}

func buildVersions() (string, string) {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Path != commonModulePath || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "dev", ""
	}
	return info.Main.Version, info.Main.Version
}

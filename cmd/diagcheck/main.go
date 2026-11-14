package main

import (
	"fmt"
	"os"

	"github.com/arno/wslc-manager/internal/app"
)

func main() {
	a := app.New()
	a.Init()
	opt := app.DefaultDiagOptions()
	opt.SocketPath = os.Getenv("MYGO_AUTOMATION")
	text, err := a.BuildDiagnostic(opt)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERR", err)
		os.Exit(1)
	}
	fmt.Print(text)
}

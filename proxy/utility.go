package proxy

import (
	"os"
)

func sigHandler(c chan os.Signal, env *Instance) {
	for sig := range c {
		println()
		env.Shutdown(sig)
		os.Exit(0)
	}
}

package main

import (
	"runtime/debug"

	"example.com/isureal-company/isureal"
)

func main() {
	debug.SetGCPercent(400)
	debug.SetMemoryLimit(3 << 30)
	config := isureal.ConfigFromEnv()
	app := isureal.NewApp(config)
	if err := app.Run(); err != nil {
		panic("failed to start server: " + err.Error())
	}
}

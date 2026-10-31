package main

import (
	"os"
	"runtime/debug"
	"strconv"

	"example.com/isureal-company/isureal"
)

func main() {
	gogc := 200
	if v, err := strconv.Atoi(os.Getenv("ISUREAL_GOGC")); err == nil {
		gogc = v
	}
	debug.SetGCPercent(gogc)
	debug.SetMemoryLimit(2500 << 20)
	config := isureal.ConfigFromEnv()
	app := isureal.NewApp(config)
	if err := app.Run(); err != nil {
		panic("failed to start server: " + err.Error())
	}
}

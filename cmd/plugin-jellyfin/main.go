package main

import (
	"github.com/slidebolt/plugin-jellyfin/app"
	runtime "github.com/slidebolt/sb-runtime"
)

func main() { runtime.Run(app.New()) }

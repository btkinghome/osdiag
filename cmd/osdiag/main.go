package main

import (
	"os"

	"luanbird/osdiag/internal/app"
)

func main() {
	os.Exit(app.Main(os.Args[1:]))
}

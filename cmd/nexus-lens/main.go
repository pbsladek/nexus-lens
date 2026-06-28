package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("nexus-lens commands: version")
		return
	}

	switch os.Args[1] {
	case "version":
		fmt.Println("nexus-lens dev")
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(2)
	}
}

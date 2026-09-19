package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	in := flag.String("in", "", "directory or file list of broadcast audio")
	flag.Parse()

	if *in == "" {
		flag.Usage()
		os.Exit(2)
	}

	fmt.Fprintln(os.Stderr, "miner: not implemented")
	os.Exit(1)
}

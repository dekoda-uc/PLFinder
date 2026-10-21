package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// runInteractive is used when asnlookup is run with no arguments (e.g.
// double-clicking the .exe): it asks questions on the console instead of
// opening a window. It never touches any host, it only queries and lists
// ranges.
//
// At the end it waits for Enter before exiting: on Windows the console
// closes itself as soon as the program finishes, so without this pause a
// double-click looks like a flash that closes immediately, whether it
// finished fine or failed.
func runInteractive(ipVersion string, showAll bool) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("asnlookup — ip or url to look up: ")
	target := readLine(reader)
	if target == "" {
		fmt.Println("you didn't type anything, exiting.")
		waitEnter(reader)
		return
	}

	fmt.Print("ip family to list [4/6/all] (enter = 4): ")
	if v := readLine(reader); v != "" {
		ipVersion = normalizeIPVersion(v)
	}

	fmt.Print("show every prefix without collapsing the smaller ones inside a bigger one? [y/N]: ")
	if ans := readLine(reader); strings.EqualFold(ans, "y") || strings.EqualFold(ans, "yes") {
		showAll = true
	}

	fmt.Println()
	if err := runCLI(target, ipVersion, showAll); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
	}

	fmt.Print("\nopen the results folder? [y/N]: ")
	if ans := readLine(reader); strings.EqualFold(ans, "y") || strings.EqualFold(ans, "yes") {
		if err := openFolder("output"); err != nil {
			fmt.Fprintln(os.Stderr, "couldn't open the folder:", err)
		}
	}

	waitEnter(reader)
}

func readLine(r *bufio.Reader) string {
	line, _ := r.ReadString('\n')
	return strings.TrimSpace(line)
}

func waitEnter(r *bufio.Reader) {
	fmt.Print("\npress enter to exit... ")
	r.ReadString('\n')
}

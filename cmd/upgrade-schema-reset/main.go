package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	repoRoot := flag.String("repo-root", "", "repository root to rewrite authored case and suite files")
	database := flag.String("database", "", "sqlite database to convert in place after writing a .bak copy")
	flag.Parse()
	if *repoRoot == "" && *database == "" {
		fmt.Fprintln(os.Stderr, "usage: upgrade-schema-reset [--repo-root DIR] [--database FILE]")
		os.Exit(2)
	}
	if *repoRoot != "" {
		root, err := filepath.Abs(*repoRoot)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		summary, err := upgradeAuthoredFiles(root)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("authored files: %s\n", summary)
	}
	if *database != "" {
		path, err := filepath.Abs(*database)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		summary, err := upgradeDatabase(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("database %s: %s\n", path, summary)
	}
}

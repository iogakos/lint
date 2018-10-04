// Copyright (c) 2013 The Go Authors. All rights reserved.
//
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file or at
// https://developers.google.com/open-source/licenses/bsd.

// golint lints the Go source files named on its command line.
package main

import (
	"flag"
	"fmt"
	"go/build"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/lint"
	"golang.org/x/tools/refactor/rename"
)

type lintMode []string

func (l *lintMode) String() string {
	return ""
}

func (l *lintMode) Set(v string) error {
	*l = append(*l, v)
	return nil
}

var (
	mode          lintMode
	minConfidence = flag.Float64("min_confidence", 0.8, "minimum confidence of a problem to print it")
	setExitStatus = flag.Bool("set_exit_status", false, "set exit status to 1 if any issues are found")
	fixNames      = flag.Bool("fix_names", false, "fix name problems by applying linter suggestions")
	suggestions   int
)

func init() {
	flag.Var(&mode, "only", "limit linting to only these type of errors. e.g. 'names'")
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "\tgolint [flags] # runs on package in current directory\n")
	fmt.Fprintf(os.Stderr, "\tgolint [flags] [packages]\n")
	fmt.Fprintf(os.Stderr, "\tgolint [flags] [directories] # where a '/...' suffix includes all sub-directories\n")
	fmt.Fprintf(os.Stderr, "\tgolint [flags] [files] # all must belong to a single package\n")
	fmt.Fprintf(os.Stderr, "Flags:\n")
	flag.PrintDefaults()
}

func main() {
	flag.Usage = usage
	flag.Parse()

	if flag.NArg() == 0 {
		lintDir(".")
	} else {
		// dirsRun, filesRun, and pkgsRun indicate whether golint is applied to
		// directory, file or package targets. The distinction affects which
		// checks are run. It is no valid to mix target types.
		var dirsRun, filesRun, pkgsRun int
		var args []string
		for _, arg := range flag.Args() {
			if strings.HasSuffix(arg, "/...") && isDir(arg[:len(arg)-len("/...")]) {
				dirsRun = 1
				for _, dirname := range allPackagesInFS(arg) {
					args = append(args, dirname)
				}
			} else if isDir(arg) {
				dirsRun = 1
				args = append(args, arg)
			} else if exists(arg) {
				filesRun = 1
				args = append(args, arg)
			} else {
				pkgsRun = 1
				args = append(args, arg)
			}
		}

		if dirsRun+filesRun+pkgsRun != 1 {
			usage()
			os.Exit(2)
		}
		switch {
		case dirsRun == 1:
			for _, dir := range args {
				lintDir(dir)
			}
		case filesRun == 1:
			lintFiles(args...)
		case pkgsRun == 1:
			for _, pkg := range importPaths(args) {
				lintPackage(pkg)
			}
		}
	}

	if *setExitStatus && suggestions > 0 {
		fmt.Fprintf(os.Stderr, "Found %d lint suggestions; failing.\n", suggestions)
		os.Exit(1)
	}
}

func isDir(filename string) bool {
	fi, err := os.Stat(filename)
	return err == nil && fi.IsDir()
}

func exists(filename string) bool {
	_, err := os.Stat(filename)
	return err == nil
}

type blacklist []lint.Problem

func (b *blacklist) blacklisted(p lint.Problem) bool {
	var st bool

	for _, bp := range *b {
		if bp.Category == p.Category && bp.Position.Filename == p.Position.Filename &&
			bp.Position.Line == p.Position.Line && bp.Position.Offset == p.Position.Offset &&
			bp.Position.Column == p.Position.Column {

			st = true
			break
		}
	}
	return st
}

func lintFiles(filenames ...string) {
	var bl blacklist
again:
	files := make(map[string][]byte)
	for _, filename := range filenames {
		src, err := ioutil.ReadFile(filename)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			continue
		}
		files[filename] = src
	}

	l := lint.Linter{Mode: mode}
	ps, err := l.LintFiles(files)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return
	}
	for _, p := range ps {
		if p.Confidence >= *minConfidence {
			fmt.Printf("%v: %s\n", p.Position, p.Text)
			suggestions++
		}

		if *fixNames && p.Category == "naming" {
			log.Printf("[INFO] trying to fix naming problem: %+v", p)
			offset := fmt.Sprintf("%s:#%d", p.Position.Filename, p.Position.Offset)
			if !bl.blacklisted(p) {
				upds, err := rename.Main(&build.Default, offset, "", p.ReplacementLine)
				if err != nil {
					fmt.Fprintf(os.Stderr, "fix-names: error renaming: %+v", err)
					bl = append(bl, p)
				}
				if len(upds) > 0 {
					fmt.Fprintf(os.Stderr, "fix-names: files updated: %+v", upds)
					goto again
				}
			} else {
				fmt.Fprintf(os.Stderr, "Problem is blacklisted, skipping: %+v", p)
			}
		}
	}
}

func lintDir(dirname string) {
	pkg, err := build.ImportDir(dirname, 0)
	lintImportedPackage(pkg, err)
}

func lintPackage(pkgname string) {
	pkg, err := build.Import(pkgname, ".", 0)
	lintImportedPackage(pkg, err)
}

func lintImportedPackage(pkg *build.Package, err error) {
	if err != nil {
		if _, nogo := err.(*build.NoGoError); nogo {
			// Don't complain if the failure is due to no Go source files.
			return
		}
		fmt.Fprintln(os.Stderr, err)
		return
	}

	var files []string
	files = append(files, pkg.GoFiles...)
	files = append(files, pkg.CgoFiles...)
	files = append(files, pkg.TestGoFiles...)
	if pkg.Dir != "." {
		for i, f := range files {
			files[i] = filepath.Join(pkg.Dir, f)
		}
	}
	// TODO(dsymonds): Do foo_test too (pkg.XTestGoFiles)

	for _, f := range files {
		lintFiles(f)
	}
}

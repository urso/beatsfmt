package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/tools/imports"

	"github.com/elastic/go-licenser/licensing"
)

type settings struct {
	srcDir    string
	overwrite bool
	allErrors bool
	diff      bool
	list      bool
}

type formatter func(settings, string, []byte) ([]byte, error)

var formatters = []formatter{
	licenseHeader,
	formatSource,
}

var ESLicenseHeader = []string{
	`// Licensed to Elasticsearch B.V. under one or more contributor`,
	`// license agreements. See the NOTICE file distributed with`,
	`// this work for additional information regarding copyright`,
	`// ownership. Elasticsearch B.V. licenses this file to you under`,
	`// the Apache License, Version 2.0 (the "License"); you may`,
	`// not use this file except in compliance with the License.`,
	`// You may obtain a copy of the License at`,
	`//`,
	`//     http://www.apache.org/licenses/LICENSE-2.0`,
	`//`,
	`// Unless required by applicable law or agreed to in writing,`,
	`// software distributed under the License is distributed on an`,
	`// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY`,
	`// KIND, either express or implied.  See the License for the`,
	`// specific language governing permissions and limitations`,
	`// under the License.`,
}
var headerBytes []byte

func init() {
	for _, line := range ESLicenseHeader {
		headerBytes = append(headerBytes, []byte(line)...)
		headerBytes = append(headerBytes, []byte("\n")...)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: beatfmt [flags] [path ...]\n")
	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	var settings settings
	var exitCode int

	flag.BoolVar(&settings.list, "l", false, "list files whose formatting differs only")
	flag.BoolVar(&settings.allErrors, "e", false, "report all errors (not just the first 10 error)")
	flag.BoolVar(&settings.diff, "d", false, "display diff")
	flag.BoolVar(&settings.overwrite, "w", false, "write result to source file instead of stdout")
	flag.StringVar(&settings.srcDir, "srcdir", "", "choose imports as if source code is from `dir`")

	flag.Usage = usage
	flag.Parse()
	paths := flag.Args()

	if len(paths) == 0 {
		err := processFile(settings, "<standard input>", os.Stdin, os.Stdout, true)
		if err != nil {
			exitCode = 1
		}
		os.Exit(exitCode)
	}

	for _, path := range paths {
		switch fi, err := os.Stat(path); {
		case err != nil:
			log.Println(err)
			exitCode = 1
		case fi.IsDir():
			if err := filepath.Walk(path, visitFile(settings)); err != nil {
				exitCode = 1
			}
		default:
			err := processFile(settings, path, nil, os.Stdout, false)
			if err != nil {
				log.Println(err)
				exitCode = 1
			}
		}
	}

	os.Exit(exitCode)
}

func processFile(
	settings settings,
	filename string,
	in io.Reader,
	out io.Writer,
	stdin bool,
) error {
	if in == nil {
		f, err := os.Open(filename)
		if err != nil {
			return err
		}
		defer f.Close()
		in = f
	}

	src, err := ioutil.ReadAll(in)
	if err != nil {
		return err
	}

	target := filename
	if dir := settings.srcDir; dir != "" {
		target = filepath.Join(dir, filepath.Base(filename))
	}

	res, err := applyFormatters(settings, target, src)
	if err != nil {
		return err
	}

	// no changes required
	if bytes.Equal(src, res) {
		// write if target is stdout
		if !settings.list && !settings.overwrite && !settings.diff {
			_, err := out.Write(res)
			return err
		}

		return nil
	}

	if settings.list {
		fmt.Println(out, filename)
	}
	if settings.overwrite {
		err = ioutil.WriteFile(filename, res, 0)
		if err != nil {
			return err
		}
	}
	if settings.diff {
		data, err := diff(src, res)
		if err != nil {
			return fmt.Errorf("computing diff: %s", err)
		}
		fmt.Printf("diff %v gofmt/%v\n", filename, filename)
		out.Write(data)
	}

	if !settings.list && !settings.overwrite && !settings.diff {
		out.Write(res)
	}

	return nil
}

func visitFile(settings settings) func(string, os.FileInfo, error) error {
	return func(path string, f os.FileInfo, err error) error {

		if err == nil && isGoFile(f) {
			err = processFile(settings, path, nil, os.Stdout, false)
		}
		if err != nil {
			log.Println(err)
		}
		return nil
	}
}

func applyFormatters(settings settings, filename string, src []byte) ([]byte, error) {
	contents := src
	for _, f := range formatters {
		var err error
		contents, err = f(settings, filename, contents)
		if err != nil {
			return nil, err
		}
	}

	return contents, nil
}

func licenseHeader(settings settings, filename string, src []byte) ([]byte, error) {
	buf := bytes.NewBuffer(src)
	if licensing.ContainsHeader(buf, ESLicenseHeader) {
		return src, nil
	}

	// Header is the licenser that all of the files in the repository must have.
	return licensing.RewriteWithHeader(src, headerBytes), nil
}

func formatSource(settings settings, filename string, src []byte) ([]byte, error) {
	return imports.Process(filename, src, &imports.Options{
		TabWidth:  8,
		AllErrors: settings.allErrors,
		TabIndent: true,
		Comments:  true,
		Fragment:  true,
	})
}

func isGoFile(fi os.FileInfo) bool {
	name := fi.Name()
	return !fi.IsDir() && !strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".go")
}

func diff(old, new []byte) ([]byte, error) {
	fOld, err := writeTmpFile(old)
	if err != nil {
		return nil, err
	}
	defer os.Remove(fOld)

	fNew, err := writeTmpFile(new)
	if err != nil {
		return nil, err
	}
	defer os.Remove(fNew)

	data, err := exec.Command("diff", "-u", fOld, fNew).CombinedOutput()
	if len(data) > 0 {
		err = nil
	}
	return data, err
}

func writeTmpFile(content []byte) (string, error) {
	f, err := ioutil.TempFile("", "beatfmt")
	if err != nil {
		return "", err
	}

	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(f.Name())
		}
	}()

	_, err = f.Write(content)
	if err != nil {
		return "", err
	}

	ok = true
	return f.Name(), nil
}

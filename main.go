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
	license      string
	licSearchCWD bool
	srcDir       string
	overwrite    bool
	allErrors    bool
	diff         bool
	list         bool
}

type formatter func(string, []byte) ([]byte, error)

const licenseFileName = ".go_license_header"
const xpackLicenseFileName = ".go_xpack_license_header"

func usage() {
	fmt.Fprintf(os.Stderr, "usage: beatfmt [flags] [path ...]\n")
	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	var settings settings
	var exitCode int

	// set goimports global
	flag.StringVar(&imports.LocalPrefix, "local", "", "put imports beginning with this string after 3rd-party packages")

	flag.BoolVar(&settings.list, "l", false, "list files whose formatting differs only")
	flag.BoolVar(&settings.allErrors, "e", false, "report all errors (not just the first 10 error)")
	flag.BoolVar(&settings.diff, "d", false, "display diff")
	flag.BoolVar(&settings.overwrite, "w", false, "write result to source file instead of stdout")
	flag.StringVar(&settings.srcDir, "srcdir", "", "choose imports as if source code is from `dir`")
	flag.StringVar(&settings.license, "license", "", "set source header license file")
	flag.BoolVar(&settings.licSearchCWD, "licwd", false, "search license file beginning in current working directory")
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

	target := filename
	if dir := settings.srcDir; dir != "" {
		// if srcdir is file name, use the name.
		if isFile(dir) || (!isDir(dir) && strings.HasSuffix(dir, ".go")) {
			target = dir
		} else {
			target = filepath.Join(dir, filepath.Base(filename))
		}
	}

	if settings.license == "" {
		// let's find one
		root, _ := filepath.Abs(os.Getenv("GOPATH"))
		var start string
		if settings.licSearchCWD {
			var err error
			start, err = filepath.Abs(".")
			if err != nil {
				return err
			}
		} else {
			var err error
			start, err = filepath.Abs(target)
			if err != nil {
				return err
			}
		}

		if root == "" {
			root = "."
		}

		licenseType := licenseFileName

		dir := filepath.Dir(start)
		for dir != root && dir != "/" {
			if filepath.Base(dir) == "x-pack" {
				licenseType = xpackLicenseFileName
			}

			license := filepath.Join(dir, licenseType)
			fi, err := os.Stat(license)
			if err == nil && fi.Mode().IsRegular() {
				settings.license = license
				break
			}

			dir = filepath.Dir(dir)
		}
	}

	var formatters []formatter

	if settings.license != "" {
		contents, err := ioutil.ReadFile(settings.license)
		if err != nil {
			return err
		}

		formatters = append(formatters, licenseHeader(strings.Split(string(contents), "\n")))
	}
	formatters = append(formatters, formatSource(settings.allErrors))

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

	res, err := applyFormatters(formatters, target, src)
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

func applyFormatters(formatters []formatter, filename string, src []byte) ([]byte, error) {
	contents := src
	for _, f := range formatters {
		var err error
		contents, err = f(filename, contents)
		if err != nil {
			return nil, err
		}
	}

	return contents, nil
}

func licenseHeader(header []string) func(string, []byte) ([]byte, error) {
	var headerBytes []byte
	for _, line := range header {
		headerBytes = append(headerBytes, []byte(line)...)
		headerBytes = append(headerBytes, []byte("\n")...)
	}

	return func(filename string, src []byte) ([]byte, error) {
		buf := bytes.NewBuffer(src)
		if licensing.ContainsHeader(buf, header) {
			return src, nil
		}

		// Header is the licenser that all of the files in the repository must have.
		return licensing.RewriteWithHeader(src, headerBytes), nil
	}
}

func formatSource(allErrors bool) func(string, []byte) ([]byte, error) {
	return func(filename string, src []byte) ([]byte, error) {
		return imports.Process(filename, src, &imports.Options{
			TabWidth:  8,
			AllErrors: allErrors,
			TabIndent: true,
			Comments:  true,
			Fragment:  true,
		})
	}
}

func isGoFile(fi os.FileInfo) bool {
	name := fi.Name()
	return fi.Mode().IsRegular() && !strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".go")
}

func isFile(name string) bool {
	fi, err := os.Stat(name)
	return err == nil && fi.Mode().IsRegular()
}

func isDir(name string) bool {
	fi, err := os.Stat(name)
	return err == nil && fi.IsDir()
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

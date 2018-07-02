package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"testing"
)

func TestFmt(t *testing.T) {
	const inputExt = ".input"
	const goldenExt = ".golden"

	inputs, err := filepath.Glob("testdata/*" + inputExt)
	if err != nil {
		t.Fatal(err)
	}

	testHeader := []string{
		`// License header line 1`,
		`// License header line 2`,
	}

	formatters := []formatter{
		licenseHeader(testHeader),
		formatSource(true),
	}

	for _, input := range inputs {
		input := input
		name := filepath.Base(input)
		name = name[:len(name)-len(inputExt)]

		t.Run(name, func(t *testing.T) {
			content, err := ioutil.ReadFile(input)
			if err != nil {
				t.Fatal(err)
			}

			expected, err := ioutil.ReadFile(fmt.Sprintf("testdata/%v%v", name, goldenExt))
			if err != nil {
				t.Fatal(err)
			}

			actual, err := applyFormatters(formatters, input, content)
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(expected, actual) {
				t.Errorf("formatting error.\nExpected:\n%s\n\nActual:\n%s", expected, actual)
			}
		})
	}
}

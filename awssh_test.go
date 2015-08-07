package main

import (
	"testing"
)

func TestCamelCase(t *testing.T) {
	testData := []struct {
		Input  string
		Output string
	}{
		{
			"",
			"",
		},
		{
			"-",
			"",
		},
		{
			"---",
			"",
		},
		{
			"hello-world",
			"helloWorld",
		},
		{
			"hello-",
			"hello",
		},
		{
			"-hello",
			"Hello",
		},
		{
			"a-b-c",
			"aBC",
		},
	}

	for _, d := range testData {
		cc := camelCase(d.Input)

		if cc != d.Output {
			t.Errorf("Unexpected output for input '%s': got '%s', expected '%s'", d.Input, cc, d.Output)
		}
	}
}

func TestFuzzyMatch(t *testing.T) {
	testData := []struct {
		Input   string
		Match   string
		Matches bool
	}{
		{
			"",
			"",
			true,
		},
		{
			"Hello",
			"",
			true,
		},
		{
			"",
			"match",
			false,
		},
		{
			"Hello",
			"Hello",
			true,
		},
		{
			"Hello",
			"Hlo",
			true,
		},
		{
			"HELLO",
			"hlo",
			true,
		},
	}

	for _, d := range testData {
		matches := fuzzyMatch(d.Input, d.Match)

		if d.Matches != matches {
			t.Errorf("Unexpected match result for input '%s' and match '%s': expected %v, got %v", d.Input, d.Match, d.Matches, matches)
		}
	}
}

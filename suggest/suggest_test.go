package suggest_test

import (
	"bytes"
	"flag"
	"go/build"
	"io/ioutil"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdempsky/gocode/suggest"
)

var flagTestDirs = flag.String("testdatadirs", "", "testdata subdirs")

func TestRegress(t *testing.T) {
	s := suggest.New(testing.Verbose(), &build.Default)

	var testDirs []string
	if *flagTestDirs != "" {
		testDirs = strings.Split(*flagTestDirs, ",")
	} else {
		var err error
		testDirs, err = filepath.Glob("testdata/test.*")
		if err != nil {
			t.Fatal(err)
		}
	}

	failed := 0
	for i, testDir := range testDirs {
		if !testRegress(t, s, i, testDir) {
			failed++
		}
	}
	if failed != 0 {
		t.Errorf("%d failed / %d total", failed, len(testDirs))
	}
}

func testRegress(t *testing.T, s *suggest.Suggester, num int, testDir string) bool {
	testDir, err := filepath.Abs(testDir)
	if err != nil {
		t.Errorf("Abs failed: %v", err)
		return false
	}

	filename := filepath.Join(testDir, "test.go.in")
	file, err := ioutil.ReadFile(filename)
	if err != nil {
		t.Errorf("ReadFile failed: %v", err)
		return false
	}

	cursor := bytes.IndexByte(file, '@')
	if cursor < 0 {
		t.Errorf("Missing @")
		return false
	}
	file = append(file[:cursor], file[cursor+1:]...)

	candidates, prefixLen := s.Suggest(file, filename, cursor)

	var out bytes.Buffer
	suggest.NiceFormat(&out, candidates, prefixLen)

	want, err := ioutil.ReadFile(filepath.Join(testDir, "out.expected"))
	if got := out.Bytes(); !bytes.Equal(got, want) {
		t.Errorf("%s:\nGot:\n%s\nWant:\n%s\n", testDir, got, want)
		return false
	}

	return true
}

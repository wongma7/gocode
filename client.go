package main

import (
	"flag"
	"fmt"
	"go/build"
	"io/ioutil"
	"log"
	"net/rpc"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/mdempsky/gocode/gbimporter"
	"github.com/mdempsky/gocode/suggest"
)

func doClient() {
	addr := *g_addr
	if *g_sock == "unix" {
		addr = getSocketPath()
	}

	// client
	client, err := rpc.Dial(*g_sock, addr)
	if err != nil {
		if *g_sock == "unix" && fileExists(addr) {
			os.Remove(addr)
		}

		err = tryStartServer()
		if err != nil {
			log.Fatal(err)
		}
		client, err = tryToConnect(*g_sock, addr)
		if err != nil {
			log.Fatal(err)
		}
	}
	defer client.Close()

	if flag.NArg() > 0 {
		switch flag.Arg(0) {
		case "autocomplete":
			cmdAutoComplete(client)
		case "close", "exit":
			cmdExit(client)
		default:
			fmt.Printf("gocode: unknown subcommand: %q\nRun 'gocode -help' for usage.\n", flag.Arg(0))
		}
	}
}

func tryStartServer() error {
	path := get_executable_filename()
	args := []string{os.Args[0], "-s", "-sock", *g_sock, "-addr", *g_addr}
	cwd, _ := os.Getwd()

	var err error
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	stdout, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	stderr, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return err
	}

	procattr := os.ProcAttr{Dir: cwd, Env: os.Environ(), Files: []*os.File{stdin, stdout, stderr}}
	p, err := os.StartProcess(path, args, &procattr)
	if err != nil {
		return err
	}

	return p.Release()
}

func tryToConnect(network, address string) (*rpc.Client, error) {
	start := time.Now()
	for {
		client, err := rpc.Dial(network, address)
		if err != nil && time.Since(start) < time.Second {
			continue
		}
		return client, err
	}
}

func cmdAutoComplete(c *rpc.Client) {
	var req AutoCompleteRequest
	req.Filename, req.Data, req.Cursor = prepareFilenameDataCursor()
	req.Context = gbimporter.PackContext(&build.Default)

	var res AutoCompleteReply
	if err := c.Call("Server.AutoComplete", &req, &res); err != nil {
		panic(err)
	}

	fmt := suggest.Formatters[*g_format]
	if fmt == nil {
		fmt = suggest.NiceFormat
	}
	fmt(os.Stdout, res.Candidates, res.Len)
}

func cmdExit(c *rpc.Client) {
	var req ExitRequest
	var res ExitReply
	if err := c.Call("Server.Exit", &req, &res); err != nil {
		panic(err)
	}
}

func prepareFilenameDataCursor() (string, []byte, int) {
	var file []byte
	var err error

	if *g_input != "" {
		file, err = ioutil.ReadFile(*g_input)
	} else {
		file, err = ioutil.ReadAll(os.Stdin)
	}

	if err != nil {
		panic(err.Error())
	}

	filename := *g_input
	offset := ""
	switch flag.NArg() {
	case 2:
		offset = flag.Arg(1)
	case 3:
		filename = flag.Arg(1) // Override default filename
		offset = flag.Arg(2)
	}

	if filename != "" {
		filename, _ = filepath.Abs(filename)
	}

	cursor := -1
	if offset != "" {
		if offset[0] == 'c' || offset[0] == 'C' {
			cursor, _ = strconv.Atoi(offset[1:])
			cursor = runeToByteOffset(file, cursor)
		} else {
			cursor, _ = strconv.Atoi(offset)
		}
	}

	return filename, file, cursor
}

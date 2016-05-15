package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/build"
	"io/ioutil"
	"net/rpc"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/mdempsky/gocode/suggest"
)

func doClient() int {
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

		err = try_run_server()
		if err != nil {
			fmt.Printf("%s\n", err.Error())
			return 1
		}
		client, err = try_to_connect(*g_sock, addr)
		if err != nil {
			fmt.Printf("%s\n", err.Error())
			return 1
		}
	}
	defer client.Close()

	if flag.NArg() > 0 {
		switch flag.Arg(0) {
		case "autocomplete":
			cmd_auto_complete(client)
		case "close", "exit":
			cmd_exit(client)
		default:
			fmt.Printf("gocode: unknown subcommand: %q\nRun 'gocode -help' for usage.\n", flag.Arg(0))
		}
	}
	return 0
}

func try_run_server() error {
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

func try_to_connect(network, address string) (client *rpc.Client, err error) {
	t := 0
	for {
		client, err = rpc.Dial(network, address)
		if err != nil && t < 1000 {
			time.Sleep(10 * time.Millisecond)
			t += 10
			continue
		}
		break
	}

	return
}

func prepare_file_filename_cursor() ([]byte, string, int) {
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

	trimmed := trimShebang(file)
	cursor -= len(file) - len(trimmed)
	return trimmed, filename, cursor
}

// Commands

func cmd_auto_complete(c *rpc.Client) {
	context := packContext(&build.Default)
	file, filename, cursor := prepare_file_filename_cursor()
	fmt := suggest.Formatters[*g_format]
	if fmt == nil {
		fmt = suggest.NiceFormat
	}
	candidates, len := client_auto_complete(c, file, filename, cursor, context)
	fmt(os.Stdout, candidates, len)
}

func cmd_exit(c *rpc.Client) {
	client_exit(c, 0)
}

// returns truncated 'data' and amount of bytes skipped (for cursor pos adjustment)
func trimShebang(data []byte) []byte {
	if !bytes.HasPrefix(data, []byte("#!")) {
		return data
	}
	nl := bytes.IndexByte(data, '\n')
	if nl < 0 {
		return nil
	}
	return data[nl+1:]
}

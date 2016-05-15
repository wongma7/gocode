package main

import (
	"bytes"
	"fmt"
	"go/build"
	"log"
	"net"
	"net/rpc"
	"os"
	"runtime/debug"
	"time"

	"github.com/mdempsky/gocode/suggest"
)

func doServer() {
	addr := *g_addr
	if *g_sock == "unix" {
		addr = getSocketPath()
		if fileExists(addr) {
			log.Printf("unix socket: '%s' already exists\n", addr)
		}
	}

	lis, err := net.Listen(*g_sock, addr)
	if err != nil {
		panic(err)
	}

	if *g_sock == "unix" {
		// cleanup unix socket file
		defer os.Remove(addr)
	}

	g_daemon = newDaemon()
	rpc.Register(new(RPC))
	rpc.Accept(lis)
}

func newDaemon() *daemon {
	d := new(daemon)
	d.suggester = suggest.New(*g_debug, &d.context)
	return d
}

type daemon struct {
	context   build.Context
	suggester *suggest.Suggester
}

var g_daemon *daemon

func server_auto_complete(file []byte, filename string, cursor int, context_packed packedContext) (c []suggest.Candidate, d int) {
	fmt.Printf("server_auto_complete call\n")

	g_daemon.context = unpackContext(&context_packed)
	defer func() {
		if err := recover(); err != nil {
			fmt.Printf("panic: %s\n\n", err)
			debug.PrintStack()

			c = []suggest.Candidate{
				{Class: "PANIC", Name: "PANIC", Type: "PANIC"},
			}
		}
	}()
	if *g_debug {
		var buf bytes.Buffer
		log.Printf("Got autocompletion request for '%s'\n", filename)
		log.Printf("Cursor at: %d\n", cursor)
		buf.WriteString("-------------------------------------------------------\n")
		buf.Write(file[:cursor])
		buf.WriteString("#")
		buf.Write(file[cursor:])
		log.Print(buf.String())
		log.Println("-------------------------------------------------------")
	}
	candidates, d := g_daemon.suggester.Suggest(file, filename, cursor)
	if *g_debug {
		log.Printf("Offset: %d\n", d)
		log.Printf("Number of candidates found: %d\n", len(candidates))
		log.Printf("Candidates are:\n")
		for _, c := range candidates {
			log.Printf("  %s\n", c.String())
		}
		log.Println("=======================================================")
	}
	return candidates, d
}

func server_exit(notused int) int {
	go func() {
		time.Sleep(time.Second)
		os.Exit(0)
	}()
	return 0
}

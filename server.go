package main

import (
	"bytes"
	"fmt"
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

	rpc.Register(&Server{})
	rpc.Accept(lis)
}

type Server struct {
}

type AutoCompleteRequest struct {
	Filename string
	Data     []byte
	Cursor   int
	Context  PackedContext
}

type AutoCompleteReply struct {
	Candidates []suggest.Candidate
	Len        int
}

func (s *Server) AutoComplete(req *AutoCompleteRequest, res *AutoCompleteReply) error {
	context := unpackContext(&req.Context)
	defer func() {
		if err := recover(); err != nil {
			fmt.Printf("panic: %s\n\n", err)
			debug.PrintStack()

			res.Candidates = []suggest.Candidate{
				{Class: "PANIC", Name: "PANIC", Type: "PANIC"},
			}
		}
	}()
	if *g_debug {
		var buf bytes.Buffer
		log.Printf("Got autocompletion request for '%s'\n", req.Filename)
		log.Printf("Cursor at: %d\n", req.Cursor)
		buf.WriteString("-------------------------------------------------------\n")
		buf.Write(req.Data[:req.Cursor])
		buf.WriteString("#")
		buf.Write(req.Data[req.Cursor:])
		log.Print(buf.String())
		log.Println("-------------------------------------------------------")
	}
	candidates, d := suggest.New(*g_debug, &context).Suggest(req.Filename, req.Data, req.Cursor)
	if *g_debug {
		log.Printf("Offset: %d\n", res.Len)
		log.Printf("Number of candidates found: %d\n", len(candidates))
		log.Printf("Candidates are:\n")
		for _, c := range candidates {
			log.Printf("  %s\n", c.String())
		}
		log.Println("=======================================================")
	}
	res.Candidates, res.Len = candidates, d
	return nil
}

type ExitRequest struct{}
type ExitReply struct{}

func (s *Server) Exit(req *ExitRequest, res *ExitReply) error {
	go func() {
		time.Sleep(time.Second)
		os.Exit(0)
	}()
	return nil
}

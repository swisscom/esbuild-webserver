package main

import (
	"fmt"
	"github.com/alexflint/go-arg"
	"github.com/sirupsen/logrus"
	webserver "github.com/swisscom/esbuild-webserver/pkg"
)

var args struct {
	Endpoints []string `arg:"-e,--endpoint,separate,required"`
	Listen    string   `arg:"-l,--listen" default:"127.0.0.1:8080"`
}

func main() {
	arg.MustParse(&args)

	s, err := webserver.New(args.Endpoints)
	if err != nil {
		logrus.Fatalf("unable to create webserver: %v", err)
	}

	fmt.Printf("listening on %v\n", args.Listen)
	logrus.Fatal(s.Start(args.Listen))
}

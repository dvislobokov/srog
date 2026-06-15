package main

import (
	"errors"
	"srog"
)

type Person struct {
	Name string
	Age  int
}

func main() {
	log := srog.MustNew(
		srog.WithConsole(
			srog.MinLevel(srog.InformationLevel),
		),
		srog.WithFile("./log.logs"),
		srog.WithStackTrace(true),
		srog.WithCaller(true),
	)

	log.Error(errors.New("some error"), "something bad happened")
}

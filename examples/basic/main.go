// Command basic is a quickstart tour of srog: levels, the Serilog-style message
// template grammar (named/positional holes, @destructure, $stringify, format and
// alignment), enrichment, and error stacks.
//
//	go run .
package main

import (
	"errors"
	"time"

	"github.com/dvislobokov/srog"
)

type User struct {
	Name  string
	Email string
	Roles []string
}

func main() {
	log := srog.MustNew(
		srog.WithConsole(srog.MinLevel(srog.VerboseLevel)),
		srog.WithCaller(true),
		srog.WithStackTrace(true),
		srog.WithFile("./log.jsonnd", srog.AsJSON(), srog.MinLevel(srog.VerboseLevel)),
	)
	defer log.Close()

	// --- Levels (Serilog ladder) ---
	log.Verbose("cache warm starting")
	log.Debug("connecting to {Host}:{Port}", "db.internal", 5432)
	log.Information("service {Service} started in {Elapsed}", "billing", 42*time.Millisecond)
	log.Warning("disk usage high: {Percent}%", 87)
	log.Error(errors.New("connection refused"), "cannot reach {Dependency}", "redis")

	// --- Message templates ---
	// Named holes become structured fields; format specifiers and alignment work.
	log.Information("order {OrderId} total {Amount:.2f} at {At:HH:mm:ss}", 7, 19.5, time.Now())
	// Positional holes.
	log.Information("copied {0} of {1} files", 3, 10)
	// @ destructures a value into fields AND renders field names in the message.
	log.Information("user {@User} signed in", User{Name: "neo", Email: "neo@example.com", Roles: []string{"admin", "ops"}})
	// $ forces the scalar string form.
	log.Information("received raw id {$Id}", 42)
	// Surplus args are kept as extra_N — nothing is silently dropped.
	log.Information("just {A}", 1, 2, 3)

	// --- Enrichment: Named + ForContext (every event carries these fields) ---
	svc := log.Named("billing").ForContext("Region", "eu-west-1")
	svc.Information("charged {Amount} to {UserId}", 999, "u-42")
	svc.Warning("rate limit near for {UserId}", "u-42")
}

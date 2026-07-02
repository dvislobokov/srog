// Command echo-example wires srog into an Echo (labstack/echo/v4) application
// using the dedicated github.com/dvislobokov/srog/srogecho integration module —
// so echo stays out of core srog's dependencies. Run and hit the routes:
//
//	go mod tidy && go run .
//	curl -i localhost:8080/users/7      # 200
//	curl -i localhost:8080/users/0      # 404 via echo.HTTPError
//	curl -i localhost:8080/boom         # 500 via a returned plain error
//	curl -i localhost:8080/panic        # 500, panic recovered & logged by srog
package main

import (
	"errors"
	"net/http"

	"github.com/dvislobokov/srog"
	"github.com/dvislobokov/srog/srogecho"
	"github.com/labstack/echo/v4"
)

func main() {
	log := srog.MustNew(
		srog.WithConsole(srog.MinLevel(srog.DebugLevel)),
		// Async file sink keeps disk I/O off the request path; ECS would be
		// srog.AsECS() for direct Elasticsearch indexing.
		srog.WithFile("./echo.logs", srog.MinLevel(srog.InformationLevel), srog.Async(0)),
		srog.WithCaller(true),
		srog.WithStackTrace(true),
		srog.WithTimeFormat(srog.TimeRFC3339Nano),
	)
	defer log.Close()

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	e.Use(srogecho.Middleware(log)) // request-scoped logger + access log
	e.Use(srogecho.Recover(log))    // panics -> srog with the real stack

	e.GET("/users/:id", getUser)
	e.GET("/boom", boom)
	e.GET("/panic", func(c echo.Context) error { panic("unexpected nil map write") })

	if err := e.Start(":8080"); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err, "server stopped")
	}
}

func getUser(c echo.Context) error {
	id := c.Param("id")
	srogecho.From(c).Information("fetching user {UserId}", id)

	if id == "0" {
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	}
	return c.JSON(http.StatusOK, map[string]any{"id": id, "name": "Neo"})
}

func boom(c echo.Context) error {
	// Plain error -> middleware materializes the status and logs it at Error.
	return errors.New("kaboom while loading dashboard")
}

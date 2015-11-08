package app

import (
	"io"
	"net/http"

	"github.com/jmoiron/sqlx"
	"github.com/metcalf/saypi/auth"
	"github.com/metcalf/saypi/mux"
)

// Configuration represents the configuration for an App
type Configuration struct {
	DBDSN     string // postgres data source name
	DBMaxIdle int    // maximum number of idle DB connections
	DBMaxOpen int    // maximum number of open DB connections

	UserSecret []byte // secret for generating secure user tokens
}

// App encapsulates the handlers for the saypi API
type App struct {
	Srv     http.Handler
	closers []io.Closer
}

// Close cleans up any resources used by the app such as database connections.
func (a *App) Close() error {
	return closeAll(a.closers)
}

// New creates an App for the given configuration.
func New(config *Configuration) (*App, error) {
	var app App

	db, err := buildDB(config.DBDSN, config.DBMaxIdle, config.DBMaxOpen)
	if err != nil {
		defer app.Close()
		return nil, err
	}
	app.closers = append(app.closers, db)

	authCtrl := auth.New(config.UserSecret)

	mainMux := mux.New()
	privMux := mux.New()
	mainMux.NotFoundHandler = authCtrl.WrapC(privMux)

	mainMux.RouteFuncC("POST", "/users", authCtrl.CreateUser)
	mainMux.RouteFuncC("GET", "/users/:id", authCtrl.GetUser)

	/*
		privMux.RouteFuncC("GET", "/animals", sayCtrl.GetAnimals)

		privMux.RouteFuncC("GET", "/moods", sayCtrl.ListMoods)
		privMux.RouteFuncC("PUT", "/moods/:name", sayCtrl.SetMood)
		privMux.RouteFuncC("GET", "/moods/:name", sayCtrl.GetMood)
		privMux.RouteFuncC("DELETE", "/moods/:name", sayCtrl.DeleteMood)

		privMux.RouteFuncC("GET", "/conversations", sayCtrl.ListConversations)
		privMux.RouteFuncC("PUT", "/conversations/:name", sayCtrl.SetConversation)
		privMux.RouteFuncC("GET", "/conversations/:name", sayCtrl.GetConversation)
		privMux.RouteFuncC("DELETE", "/conversations/:name", sayCtrl.DeleteConversation)

		privMux.RouteFuncC("POST", "/conversations/:name/lines", sayCtrl.CreateLine)
		privMux.RouteFuncC("GET", "/conversations/:name/lines/:id", sayCtrl.GetLine)
		privMux.RouteFunc("DELETE", "/conversations/:name/lines/:id", sayCtrl.DeleteLine)
	*/

	// TODO: Wrap with error handling and logging
	app.Srv = mainMux

	return nil, nil
}

func buildDB(dsn string, maxIdle, maxOpen int) (*sqlx.DB, error) {
	db, err := sqlx.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxIdleConns(maxIdle)
	db.SetMaxOpenConns(maxOpen)
	return db, nil
}

func closeAll(closers []io.Closer) error {
	for _, cls := range closers {
		if err := cls.Close(); err != nil {
			return err
		}
	}
	return nil
}
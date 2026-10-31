package main

import (
	"log"

	"github.com/egoist/mygo"

	"fx-desktop/internal/app"
)

func main() {
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}

	coord := app.NewCoordinator()

	mygo.App.OnOpenURL(func(raw string) {
		if path, ok := app.ProjectFromURL(raw); ok {
			coord.OpenProject(path)
		}
	})
	mygo.App.OnSecondInstance(func(args []string, wd string) {
		coord.HandleSecondInstance(args, wd)
	})

	mygo.App.WhenReady(func() {
		coord.Setup()
		coord.Launch()
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

package main

import (
	"log"
	"os"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"knack/internal/app"
)

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	application := app.New(home)
	if wd, err := os.Getwd(); err == nil {
		application.SetWorkDir(wd)
	}
	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     "Knack",
			Width:     1180,
			Height:    760,
			MinWidth:  880,
			MinHeight: 560,
			// Traffic lights sit over the sidebar. The sidebar draws no
			// fill, so the system sidebar material shows through; the
			// content pane stays opaque.
			TitleBarStyle:  mygo.TitleBarHiddenInset,
			TitleBarHeight: 52,
			Vibrancy:       mygo.VibrancySidebar,
			StateKey:       "main",
			Content:        ui.View(application.View),
		})
		application.Attach(win)
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

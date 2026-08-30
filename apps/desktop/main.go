package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var frontendAssets embed.FS

func main() {
	report := func(err error) {
		if err != nil {
			log.Printf("desktop: %v", err)
		}
	}
	app := newDesktopApp(newProductionInitializer(defaultProductionOptions()))
	if err := wails.Run(desktopOptions(app, frontendAssets, report)); err != nil {
		report(fmt.Errorf("run Wails desktop shell: %w", err))
	}
}

func desktopOptions(app *DesktopApp, assets fs.FS, report func(error)) *options.App {
	app.setErrorReporter(report)
	return &options.App{
		Title:            "llm-studio",
		Width:            1360,
		Height:           820,
		MinWidth:         960,
		MinHeight:        640,
		BackgroundColour: options.NewRGB(26, 26, 26),
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: app.onStartup,
		OnShutdown: func(context.Context) {
			if err := app.shutdown(); err != nil && report != nil {
				report(err)
			}
		},
		Bind:           []interface{}{app},
		ErrorFormatter: desktopErrorFormatter(report),
	}
}

type desktopErrorPayload struct {
	Code string `json:"code"`
}

func desktopErrorFormatter(report func(error)) options.ErrorFormatter {
	return func(err error) any {
		var bindingError DesktopBindingError
		if errors.As(err, &bindingError) && isDesktopBindingCode(bindingError.Code) {
			return desktopErrorPayload{Code: bindingError.Code}
		}
		if err != nil && report != nil {
			report(err)
		}
		return desktopErrorPayload{Code: desktopCodeOperationFailed}
	}
}

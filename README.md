# Locorum

*Note: This is a very early prototype. It is not yet ready for use.*

## About

Locorum is a simple yet powerful local development environment for WordPress projects.

You can configure the project by editing `wails.json`. More information about the project settings can be found
here: https://wails.io/docs/reference/project-config

## Live Development

To run in live development mode, run `wails dev` in the project directory. This will run a Vite development
server that will provide very fast hot reload of your frontend changes. If you want to develop in a browser
and have access to your Go methods, there is also a dev server that runs on http://localhost:34115. Connect
to this in your browser, and you can call your Go code from devtools.

## Building

To build a redistributable, production mode package, use `wails build`.


## Migrations

You first need to install the migrations tool:

```bash
go install -tags 'sqlite' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
```

Then you can run create migrations with:

```bash
migrate create -ext sql -dir migrations create_{table_name}_table
```

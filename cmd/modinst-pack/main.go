// Command modinst-pack builds modpacks for the Crackers Modinst remote
// library. It is an authoring tool and is not shipped to players.
//
//	modinst-pack init  <pack-dir>                 create a pack folder with a pack.modinst template
//	modinst-pack build <pack-dir> <library-dir>   write the manifest, zips and index.json entry
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Knurobroddy/crackers-tui/internal/packer"
)

const usage = `usage:
  modinst-pack init  <pack-dir>                 create a pack folder with a pack.modinst template
  modinst-pack build <pack-dir> <library-dir>   build the pack into the library folder

A pack folder holds pack.modinst plus common/, windows/ and linux/ folders that
mirror the game root. The library folder is what you upload: games.json,
index.json, packs/ and files/.
`

// errUsage means no command was given; main prints the usage and exits with 2.
var errUsage = errors.New("usage")

func main() {
	err := run(os.Args[1:])
	switch {
	case errors.Is(err, errUsage):
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	case err != nil:
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	switch args[0] {
	case "init":
		return runInit(args[1:])
	case "build":
		return runBuild(args[1:])
	}
	return fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
}

// runInit runs "modinst-pack init <pack-dir>".
func runInit(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("init needs <pack-dir>\n\n%s", usage)
	}
	metaPath, err := packer.Init(args[0])
	if err != nil {
		return err
	}
	fmt.Printf("created %s\nPut mod files under %s (mirroring the game folder), edit pack.modinst, then run build.\n",
		metaPath, filepath.Join(args[0], "common"))
	return nil
}

// runBuild runs "modinst-pack build <pack-dir> <library-dir>".
func runBuild(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("build needs <pack-dir> <library-dir>\n\n%s", usage)
	}
	b := &packer.Builder{Out: os.Stdout}
	res, err := b.Build(context.Background(), args[0], args[1])
	if err != nil {
		return err
	}
	for _, name := range res.Ignored {
		fmt.Printf("note: %s is not in common/, windows/ or linux/ and was not packed\n", name)
	}
	fmt.Printf("wrote %s (version %s)\n", res.ManifestPath, res.Manifest.Version)
	for _, u := range res.Uploads {
		fmt.Printf("new file %s\n", u)
	}
	fmt.Printf("updated %s\n", filepath.Join(args[1], "index.json"))
	fmt.Println("Upload the library folder (keep older files/ for a while: clients may still hold the previous manifest).")
	return nil
}

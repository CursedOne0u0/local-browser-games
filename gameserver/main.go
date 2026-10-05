// Universal LAN gameserver launcher: one binary, every game choosable.
// Usage: gameserver [-game bomb|tank|pong|echo|horde|bastion|all] [-port N] [-no-browser]
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"local-browser-games/gameserver/bomb"
	"local-browser-games/gameserver/echo"
	"local-browser-games/gameserver/horde"
	"local-browser-games/gameserver/pong"
	"local-browser-games/gameserver/tank"
)

type entry struct {
	key   string
	title string
	def   int
	run   func(port int)
}

var games = []entry{
	{"1", "Neon Pong", 3000, pong.Run},
	{"2", "Tank Duel", 3001, tank.Run},
	{"3", "Bomb Tag", 3002, bomb.Run},
	{"4", "Echo Hunt", 3003, echo.Run},
	{"5", "Neon Horde", 3004, horde.Run},
	{"6", "Bastion LAN", 3005, nil},
}

func main() {
	gameFlag := flag.String("game", "", "game key (1-6) or 'all'")
	portFlag := flag.Int("port", 0, "override port (single game only)")
	noBrowser := flag.Bool("no-browser", false, "don't open a browser")
	flag.Parse()

	if *gameFlag == "" {
		menu(*noBrowser)
		return
	}
	if *gameFlag == "all" {
		for _, g := range games {
			if g.run == nil {
				fmt.Printf("(%s %s: Python server.py still — Go port coming)\n", g.key, g.title)
				continue
			}
			go g.run(g.def)
			fmt.Printf("started %s on :%d\n", g.title, g.def)
		}
		if !*noBrowser {
			openBrowser("http://localhost:3002")
		}
		select {}
	}
	for _, g := range games {
		if *gameFlag == g.key {
			if g.run == nil {
				fmt.Printf("%s: Go port not built yet — run python3 server.py in its folder.\n", g.title)
				os.Exit(1)
			}
			port := g.def
			if *portFlag != 0 {
				port = *portFlag
			}
			if !*noBrowser {
				go openBrowser(fmt.Sprintf("http://localhost:%d", port))
			}
			g.run(port)
			return
		}
	}
	fmt.Println("unknown game:", *gameFlag)
	os.Exit(1)
}

func menu(noBrowser bool) {
	fmt.Println("\n🎮 LOCAL BROWSER GAMES")
	for _, g := range games {
		note := ""
		if g.run == nil {
			note = "  (python for now)"
		}
		fmt.Printf(" %s) %-14s (:%d)%s\n", g.key, g.title, g.def, note)
	}
	fmt.Println(" a) ALL   q) quit")
	fmt.Print("> ")
	in, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	in = strings.TrimSpace(strings.ToLower(in))
	if in == "q" || in == "" {
		return
	}
	if in == "a" {
		for _, g := range games {
			if g.run == nil {
				continue
			}
			go g.run(g.def)
			fmt.Printf("started %s on :%d\n", g.title, g.def)
		}
		if !noBrowser {
			openBrowser("http://localhost:3002")
		}
		select {}
	}
	for _, g := range games {
		if in == g.key {
			if g.run == nil {
				fmt.Printf("%s: Go port not built yet.\n", g.title)
				return
			}
			if !noBrowser {
				go openBrowser(fmt.Sprintf("http://localhost:%d", g.def))
			}
			g.run(g.def)
			return
		}
	}
	fmt.Println("unknown choice")
}

func openBrowser(url string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		c = exec.Command("open", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	_ = c.Start()
	fmt.Println("Open", url, "in your browser")
}

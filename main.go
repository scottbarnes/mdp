package main

import (
	"bytes"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/websocket"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	emoji "github.com/yuin/goldmark-emoji"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
)

const (
	defaultTemplate = `<!DOCTYPE html>
<html>
  <head>
    <meta http-equiv="content-type" content="text/html; charset=utf-8">
    <title>{{ .Title }}</title>
    <script>
    // Preserve scroll position on window.reload();
    document.addEventListener("DOMContentLoaded", function(event) { 
      var scrollpos = sessionStorage.getItem('scrollpos');
      if (scrollpos) window.scrollTo(0, scrollpos);
    });

    window.onbeforeunload = function(e) {
      sessionStorage.setItem('scrollpos', window.scrollY);
    };

    // Create the websocket for communicating with the API.
    function connect() {
      var socket = new WebSocket("ws://localhost:5052/websocket");

      socket.onclose = function(event) {
        console.log("Websocket connection closed or unable to connect; " +
        "starting reconnect timeout");

        // Allow the last socket to be cleaned up.
        socket = null;

        // Set an interval to continue trying to reconnect
        // periodically until we succeed.
        setTimeout(function() {
          connect();
        }, 5000)
      }

      socket.onmessage = function(event) {
        var data = JSON.parse(event.data);
        switch(data.type) {
        case "build_complete":
          // 1000 = "Normal closure" and the second parameter is a
          // human-readable reason.
          socket.close(1000, "Reloading page after receiving build_complete");

          console.log("Reloading page after receiving build_complete");
          location.reload();

          break;

        default:
          console.log("Don't know how to handle type");
        }
      }
    }

    connect();
    </script>
  </head>
  <body>
{{ .FileName }}
  <hr>
{{ .Body }}
  </body>
</html>
`
)

type content struct {
	FileName string
	Title    string
	Body     template.HTML
}

type markdownHandler struct {
	body     []byte
	filename string
	tFname   string
}

type websocketEvent struct {
	Type string `json:"type"`
}

var (
	rebuild  = make(chan bool)
	serverUp = make(chan bool)
	port     = ":5052"
)

func main() {
	inFilename := flag.String("infile", "", "Markdown file to preview")
	tentativeOutFilename, _, _ := strings.Cut(*inFilename, ".")
	outFilename := flag.String("outfile", tentativeOutFilename, "Optional output HTML file")
	tFname := flag.String("t", "", "Alternate template name")
	flag.Parse()

	// If user did not provide input file, show usage
	if *inFilename == "" {
		flag.Usage()
		os.Exit(1)
	}

	if err := run(*inFilename, *outFilename, *tFname); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(inFilename string, outFilename string, tFname string) error {
	input, err := os.ReadFile(inFilename)
	if err != nil {
		return err
	}

	htmlData, err := parseContent(input, inFilename, tFname)
	if err != nil {
		return err
	}

	// If an output file is specified, write it and return here.
	if outFilename != "" {
		if err := saveHTML(outFilename, htmlData); err != nil {
			return err
		}

		return nil
	}

	// Not writing a file, so start monitoring tasks and open a preview of the watched file in a browser.
	go fileWatcher(inFilename, rebuild)
	go serveContent(inFilename, tFname)
	fmt.Println("Server listening on http://localhost" + port + "/content")
	host := "http://localhost:5052/content"
	preview(host)

	// Blocks as rebuild chan doesn't close.
	for range rebuild {
		serveContent(inFilename, tFname)
		// currentTime := time.Now()
		// fmt.Printf("%d:%d:%d: Reloaded %s\n", currentTime.Hour(), currentTime.Minute(), currentTime.Second(), inFilename)
	}

	return nil
}

func fileWatcher(file string, rebuild chan<- bool) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Has(fsnotify.Write) {
					rebuild <- true
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("error:", err)
			}
		}
	}()

	err = watcher.Add(file)
	if err != nil {
		log.Fatal(err)
	}

	<-make(chan struct{})

	return nil
}

func (md markdownHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	newInput, err := os.ReadFile(md.filename)
	if err != nil {
		return
	}
	newHTMLData, err := parseContent(newInput, md.filename, md.tFname)
	if err != nil {
		return
	}

	w.Write(newHTMLData)
}

func ws(w http.ResponseWriter, r *http.Request) {
	// This should probably iterate over trusted origins....
	// See https://github.com/gorilla/websocket/issues/367
	upgrader := websocket.Upgrader{}
	upgrader.CheckOrigin = func(r *http.Request) bool { return true }
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Print("upgrade:", err)
		return
	}
	defer c.Close()

	// Monitor for the server being up.
	up := <-serverUp
	if up {
		c.WriteJSON(websocketEvent{Type: "build_complete"})
	}
}

func serveContent(filename string, tFname string) {
	md := markdownHandler{
		body:     []byte{},
		filename: filename,
		tFname:   tFname,
	}

	mux := http.NewServeMux()
	mux.Handle("/content", md)
	mux.HandleFunc("/websocket", ws)

	http.ListenAndServe(port, mux)
	serverUp <- true
}

// Parse the markdown file using markdown and bluemonday
// to generate a valid and safe HTML file.
func parseContent(input []byte, inFilename string, tFname string) ([]byte, error) {
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			emoji.Emoji,
			highlighting.NewHighlighting(
				highlighting.WithStyle("monokai"),
			),
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
	)
	var output bytes.Buffer
	if err := md.Convert(input, &output); err != nil {
		return nil, err
	}

	body := bluemonday.UGCPolicy().SanitizeBytes(output.Bytes())

	// Parse the contents of the defaultTemplate const into a new Template
	t, err := template.New("mdp").Parse(defaultTemplate)
	if err != nil {
		return nil, err
	}

	// If user provided alternate template file, replace template
	if tFname != "" {
		t, err = template.ParseFiles(tFname)
		if err != nil {
			return nil, err
		}
	}

	c := content{
		FileName: inFilename,
		Title:    "Markdown Preview Tool",
		Body:     template.HTML(body),
	}

	// Create a buffer of bytes to write to file
	var buffer bytes.Buffer

	if err := t.Execute(&buffer, c); err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

func saveHTML(outFname string, data []byte) error {
	return os.WriteFile(outFname, data, 0o644)
}

func preview(fname string) error {
	cName := ""
	cParams := []string{}

	// Define executable based on OS
	switch runtime.GOOS {
	case "linux":
		cName = "xdg-open"
	case "windows":
		cName = "cmd.exe"
		cParams = []string{"/C", "start"}
	case "darwin":
		cName = "open"
	default:
		return fmt.Errorf("OS not supported")
	}

	// Append filename to parameters slice
	cParams = append(cParams, fname)

	// Locate executable in PATH
	cPath, err := exec.LookPath(cName)
	if err != nil {
		return err
	}

	// Open the file using the default program
	err = exec.Command(cPath, cParams...).Run()

	return err
}

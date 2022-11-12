Markdown Previewer
==================
This does two things:

1. Monitor a markdown file using [fsnotify](https://github.com/fsnotify/fsnotify), convert it to HTML on save using [goldmark](https://github.com/yuin/goldmark) and [bluemonday](https://github.com/microcosm-cc/bluemonday) (to sanitize the HTML), and display/update the display in a web browser at http://localhost:5052/content; or
1. Convert a markdown file to an HTML file using the above tools.

# Usage
## Monitor an HTML file and display it in a web browser
```
❯ ./mdp -infile README.md
Server listening on http://localhost:5052/content
```
After this, the preview should open in a new browser tab and auto-update each time the file is saved.

## Convert a markdown file to an HTML file
```
❯ ./mdp -infile README.md -outfile readme.html
```

# Installation
## Install Go if needed
- If using brew: `brew update` and `brew install go` (or `brew update go` to get go 1.19)
- Without brew, see directions at https://go.dev/doc/install

## Get code and compile
1. `git clone https://github.com/scottbarnes/mdp.git`
1. `cd mdp`
1. `go build`
1. Run with `./mdp -infile <filename.md>`

## Help
```
❯ ./mdp -h 
  -infile string
        Markdown file to preview
  -outfile string
        Optional output HTML file
  -t string
        Alternate template name
```

package bruno

import (
	"fmt"
	"regexp"
	"strings"
)

// block is one block of a .bru file. The language has three kinds:
//
//	headers {              a dictionary: pairs
//	  accept: text/plain
//	  ~x-debug: 1          (~ turns a pair off)
//	}
//
//	body:json {            text: everything up to the closing brace,
//	  {"a": 1}             without the two-space indent
//	}
//
//	vars:secret [          a list
//	  token
//	]
//
// An environment file also has lines of its own, as "color: red"; they
// are blocks with a text.
type block struct {
	name  string
	pairs []pair
	text  string
	list  []string
}

// isText reports whether a block's content is text, not pairs.
func isText(name string) bool {
	switch name {
	case "body", "body:json", "body:text", "body:xml", "body:sparql", "body:graphql", "body:graphql:vars",
		"tests", "docs", "example":
		return true
	}
	return strings.HasPrefix(name, "script:")
}

// parse reads a .bru file. Errors name the line.
func parse(src string) ([]block, error) {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var blocks []block
	for i := 0; i < len(lines); i++ {
		head := strings.TrimSpace(lines[i])
		if head == "" {
			continue
		}
		// A block ends at the next line that starts with its closing mark:
		// what is inside is indented.
		end := func(mark string) (int, error) {
			for j := i + 1; j < len(lines); j++ {
				if strings.HasPrefix(lines[j], mark) {
					return j, nil
				}
			}
			return 0, fmt.Errorf("line %d: the block %q is not closed", i+1, head)
		}
		switch name, list, isList := strings.Cut(head, "["); {
		case strings.HasSuffix(head, "{"):
			b := block{name: strings.TrimSpace(strings.TrimSuffix(head, "{"))}
			j, err := end("}")
			if err != nil {
				return nil, err
			}
			if isText(b.name) {
				b.text = outdent(lines[i+1:j], 2)
			} else if b.pairs, err = parsePairs(lines[i+1:j], i+2); err != nil {
				return nil, err
			}
			blocks = append(blocks, b)
			i = j
		case isList:
			// On one line, "[a, b]", or over several.
			if !strings.Contains(list, "]") {
				j, err := end("]")
				if err != nil {
					return nil, err
				}
				list += "\n" + strings.Join(lines[i+1:j], "\n")
				i = j
			}
			list, _, _ = strings.Cut(list, "]")
			b := block{name: strings.TrimSpace(name)}
			for _, item := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == '\n' }) {
				if item = strings.TrimSpace(item); item != "" {
					b.list = append(b.list, item)
				}
			}
			blocks = append(blocks, b)
		default:
			name, value, ok := strings.Cut(head, ":")
			if !ok {
				return nil, fmt.Errorf("line %d: %q is not a block", i+1, head)
			}
			blocks = append(blocks, block{name: name, text: strings.TrimSpace(value)})
		}
	}
	return blocks, nil
}

// outdent removes an indent of n spaces from each line.
func outdent(lines []string, n int) string {
	indent := strings.Repeat(" ", n)
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = strings.TrimPrefix(line, indent)
	}
	return strings.Join(out, "\n")
}

// An annotation is a line above a pair that says something of it, as
// @description('...'). A key may start with @ too, and then a colon follows.
var annotation = regexp.MustCompile(`^@[^\s():]+(\(|$)`)

const fence = "'''" // opens and closes a value of several lines

// parsePairs reads the lines of a dictionary block, the first of which is
// line number first of the file.
func parsePairs(lines []string, first int) ([]pair, error) {
	var pairs []pair
	// through returns the lines from i+1 up to the one that closes a
	// fence, and that line's index.
	through := func(i int) ([]string, int, error) {
		for j := i + 1; j < len(lines); j++ {
			if strings.Contains(lines[j], fence) {
				return lines[i+1 : j], j, nil
			}
		}
		return nil, 0, fmt.Errorf("line %d: the ''' is not closed", first+i)
	}
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if annotation.MatchString(line) {
			if strings.Count(line, fence)%2 == 1 {
				_, j, err := through(i)
				if err != nil {
					return nil, err
				}
				i = j
			}
			continue
		}
		key, value, ok := cutKey(line)
		if !ok {
			return nil, fmt.Errorf("line %d: %q is not a pair, key: value", first+i, line)
		}
		p := pair{Enabled: true}
		if p.Name, ok = strings.CutPrefix(key, "~"); ok {
			p.Enabled = false
		}
		value = strings.TrimSpace(value)
		switch {
		case value == "[":
			// A list as a value, as a request's tags: nothing here uses one.
			for i++; i < len(lines) && strings.TrimSpace(lines[i]) != "]"; i++ {
			}
		case strings.HasPrefix(value, fence) && strings.Count(value, fence) == 1:
			inside, j, err := through(i)
			if err != nil {
				return nil, err
			}
			p.Value = strings.TrimSpace(outdent(inside, 4))
			i = j
		default:
			p.Value = strings.TrimSuffix(strings.TrimPrefix(value, fence), fence)
		}
		pairs = append(pairs, p)
	}
	return pairs, nil
}

// cutKey splits "key: value". A key with spaces or colons in it is written
// in quotes, after its ~ if it has one.
func cutKey(line string) (key, value string, ok bool) {
	off, rest, _ := strings.Cut(line, `"`)
	if off != "" && off != "~" {
		return strings.Cut(line, ":")
	}
	var b strings.Builder
	for i := 0; i < len(rest); i++ {
		switch {
		case rest[i] == '\\' && i+1 < len(rest) && rest[i+1] == '"':
			b.WriteByte('"')
			i++
		case rest[i] == '"':
			value, ok = strings.CutPrefix(strings.TrimSpace(rest[i+1:]), ":")
			return off + b.String(), value, ok
		default:
			b.WriteByte(rest[i])
		}
	}
	return "", "", false
}

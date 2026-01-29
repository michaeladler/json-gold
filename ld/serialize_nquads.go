// Copyright 2015-2017 Piprate Limited
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ld

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
)

// NQuadRDFSerializer parses and serializes N-Quads.
type NQuadRDFSerializer struct {
}

// Parse N-Quads from string into an RDFDataset.
func (s *NQuadRDFSerializer) Parse(input interface{}) (*RDFDataset, error) {
	return ParseNQuadsFrom(input)
}

// SerializeTo writes RDFDataset as N-Quad into a writer.
func (s *NQuadRDFSerializer) SerializeTo(w io.Writer, dataset *RDFDataset) error {
	for graphName, triples := range dataset.Graphs {
		if graphName == "@default" {
			graphName = ""
		}
		for _, triple := range triples {
			quad := toNQuad(triple, graphName)
			if _, err := fmt.Fprint(w, quad); err != nil {
				return NewJsonLdError(IOError, err)
			}
		}
	}
	return nil
}

// Serialize an RDFDataset into N-Quad string.
func (s *NQuadRDFSerializer) Serialize(dataset *RDFDataset) (interface{}, error) {
	buf := bytes.NewBuffer(nil)
	if err := s.SerializeTo(buf, dataset); err != nil {
		return nil, err
	}
	return buf.String(), nil
}

func toNQuad(triple *Quad, graphName string) string {

	s := triple.Subject
	p := triple.Predicate
	o := triple.Object

	quad := ""

	// subject is an IRI or bnode
	if IsIRI(s) {
		quad += "<" + escape(s.GetValue()) + ">"
	} else {
		quad += s.GetValue()
	}

	if IsIRI(p) {
		quad += " <" + escape(p.GetValue()) + "> "
	} else {
		quad += " " + escape(p.GetValue()) + " "
	}

	// object is IRI, bnode or literal
	if IsIRI(o) {
		quad += "<" + escape(o.GetValue()) + ">"
	} else if IsBlankNode(o) {
		quad += o.GetValue()
	} else {
		literal := o.(Literal)
		escaped := escape(literal.GetValue())
		quad += "\"" + escaped + "\""
		if literal.Datatype == RDFLangString {
			quad += "@" + literal.Language
		} else if literal.Datatype != XSDString {
			quad += "^^<" + escape(literal.Datatype) + ">"
		}
	}

	// graph
	if graphName != "" {
		if strings.Index(graphName, "_:") != 0 {
			quad += " <" + escape(graphName) + ">"
		} else {
			quad += " " + graphName
		}
	}

	quad += " .\n"

	return quad
}

func unescape(str string) string {
	str = strings.ReplaceAll(str, "\\\\", "\\")
	str = strings.ReplaceAll(str, "\\\"", "\"")
	str = strings.ReplaceAll(str, "\\n", "\n")
	str = strings.ReplaceAll(str, "\\r", "\r")
	str = strings.ReplaceAll(str, "\\t", "\t")
	return str
}

func escape(str string) string {
	str = strings.ReplaceAll(str, "\\", "\\\\")
	str = strings.ReplaceAll(str, "\"", "\\\"")
	str = strings.ReplaceAll(str, "\n", "\\n")
	str = strings.ReplaceAll(str, "\r", "\\r")
	str = strings.ReplaceAll(str, "\t", "\\t")
	return str
}

type lineScanner interface {
	Bytes() []byte
	Scan() bool
	Err() error
}

type bytesLineScanner struct {
	err   error
	b     []byte
	token []byte
	i     int
}

func (ls *bytesLineScanner) Err() error { return ls.err }
func (ls *bytesLineScanner) Scan() bool {
	b, i := ls.b, ls.i
	if ls.err != nil || i >= len(b) {
		return false
	}
	di, token, err := bufio.ScanLines(b[i:], true)
	if err != nil {
		ls.err = err
		return false
	}
	ls.token = token
	ls.i += di
	return true
}
func (ls *bytesLineScanner) Bytes() []byte {
	return ls.token
}

func newScannerFor(o interface{}) (lineScanner, error) {
	switch inp := o.(type) {
	case []byte:
		return &bytesLineScanner{b: inp}, nil
	case string:
		return &bytesLineScanner{b: []byte(inp)}, nil
	case io.Reader:
		return bufio.NewScanner(inp), nil
	default:
		return nil, NewJsonLdError(InvalidInput, "expected []byte, string or io.Reader")
	}
}

// parsedQuad holds the parsed components of an N-Quad
type parsedQuad struct {
	subjectIRI   string
	subjectBNode string
	predicate    string
	objectIRI    string
	objectBNode  string
	literal      string
	datatype     string
	language     string
	graphIRI     string
	graphBNode   string
}

// parseQuadLine parses a single N-Quad line using a custom parser instead of regex
func parseQuadLine(line []byte) (*parsedQuad, error) {
	p := &quadParser{data: line, pos: 0}
	return p.parse()
}

type quadParser struct {
	data []byte
	pos  int
}

func (p *quadParser) parse() (*parsedQuad, error) {
	quad := &parsedQuad{}

	// skip leading whitespace
	p.skipWhitespace()

	// parse subject (IRI or blank node)
	if err := p.parseSubject(quad); err != nil {
		return nil, err
	}

	// require at least one space
	if !p.skipRequiredWhitespace() {
		return nil, fmt.Errorf("expected whitespace after subject")
	}

	// parse predicate (IRI)
	if err := p.parsePredicate(quad); err != nil {
		return nil, err
	}

	// require at least one space
	if !p.skipRequiredWhitespace() {
		return nil, fmt.Errorf("expected whitespace after predicate")
	}

	// parse object (IRI, blank node, or literal)
	if err := p.parseObject(quad); err != nil {
		return nil, err
	}

	// optional whitespace
	p.skipWhitespace()

	// parse optional graph and terminating '.'
	if err := p.parseGraphAndTerminator(quad); err != nil {
		return nil, err
	}

	// skip trailing whitespace
	p.skipWhitespace()

	// ensure we consumed the entire line
	if p.pos < len(p.data) {
		return nil, fmt.Errorf("unexpected characters after quad")
	}

	return quad, nil
}

func (p *quadParser) skipWhitespace() {
	for p.pos < len(p.data) && (p.data[p.pos] == ' ' || p.data[p.pos] == '\t') {
		p.pos++
	}
}

func (p *quadParser) skipRequiredWhitespace() bool {
	start := p.pos
	p.skipWhitespace()
	return p.pos > start
}

func (p *quadParser) parseSubject(quad *parsedQuad) error {
	if p.pos >= len(p.data) {
		return fmt.Errorf("unexpected end of input")
	}

	if p.data[p.pos] == '<' {
		iri, err := p.parseIRI()
		if err != nil {
			return err
		}
		quad.subjectIRI = iri
	} else if p.pos+1 < len(p.data) && p.data[p.pos] == '_' && p.data[p.pos+1] == ':' {
		bnode, err := p.parseBlankNode()
		if err != nil {
			return err
		}
		quad.subjectBNode = bnode
	} else {
		return fmt.Errorf("invalid subject")
	}

	return nil
}

func (p *quadParser) parsePredicate(quad *parsedQuad) error {
	if p.pos >= len(p.data) || p.data[p.pos] != '<' {
		return fmt.Errorf("predicate must be an IRI")
	}

	iri, err := p.parseIRI()
	if err != nil {
		return err
	}
	quad.predicate = iri
	return nil
}

func (p *quadParser) parseObject(quad *parsedQuad) error {
	if p.pos >= len(p.data) {
		return fmt.Errorf("unexpected end of input")
	}

	if p.data[p.pos] == '<' {
		iri, err := p.parseIRI()
		if err != nil {
			return err
		}
		quad.objectIRI = iri
	} else if p.pos+1 < len(p.data) && p.data[p.pos] == '_' && p.data[p.pos+1] == ':' {
		bnode, err := p.parseBlankNode()
		if err != nil {
			return err
		}
		quad.objectBNode = bnode
	} else if p.data[p.pos] == '"' {
		if err := p.parseLiteral(quad); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("invalid object")
	}

	return nil
}

func (p *quadParser) parseIRI() (string, error) {
	if p.pos >= len(p.data) || p.data[p.pos] != '<' {
		return "", fmt.Errorf("expected '<'")
	}
	p.pos++ // skip '<'

	start := p.pos
	for p.pos < len(p.data) && p.data[p.pos] != '>' {
		p.pos++
	}

	if p.pos >= len(p.data) {
		return "", fmt.Errorf("unterminated IRI")
	}

	iri := string(p.data[start:p.pos])
	p.pos++ // skip '>'

	// Basic validation: IRI should contain a colon
	hasColon := false
	for i := 0; i < len(iri); i++ {
		if iri[i] == ':' {
			hasColon = true
			break
		}
	}
	if !hasColon {
		return "", fmt.Errorf("invalid IRI: missing colon")
	}

	return iri, nil
}

func (p *quadParser) parseBlankNode() (string, error) {
	if p.pos+1 >= len(p.data) || p.data[p.pos] != '_' || p.data[p.pos+1] != ':' {
		return "", fmt.Errorf("expected '_:'")
	}

	start := p.pos
	p.pos += 2 // skip '_:'

	if p.pos >= len(p.data) || !isBlankNodeStartChar(p.data[p.pos]) {
		return "", fmt.Errorf("invalid blank node label")
	}

	p.pos++
	for p.pos < len(p.data) && isBlankNodeChar(p.data[p.pos]) {
		p.pos++
	}

	// Handle trailing dots (not allowed at the end)
	for p.pos > start+2 && p.data[p.pos-1] == '.' {
		p.pos--
	}

	return string(p.data[start:p.pos]), nil
}

func (p *quadParser) parseLiteral(quad *parsedQuad) error {
	if p.pos >= len(p.data) || p.data[p.pos] != '"' {
		return fmt.Errorf("expected '\"'")
	}
	p.pos++ // skip opening '"'

	start := p.pos
	for p.pos < len(p.data) {
		if p.data[p.pos] == '\\' {
			// skip escaped character
			p.pos += 2
			if p.pos > len(p.data) {
				return fmt.Errorf("unterminated literal")
			}
		} else if p.data[p.pos] == '"' {
			break
		} else {
			p.pos++
		}
	}

	if p.pos >= len(p.data) {
		return fmt.Errorf("unterminated literal")
	}

	quad.literal = string(p.data[start:p.pos])
	p.pos++ // skip closing '"'

	// check for language tag or datatype
	if p.pos < len(p.data) {
		if p.data[p.pos] == '@' {
			// language tag
			p.pos++
			start := p.pos
			for p.pos < len(p.data) && isLanguageChar(p.data[p.pos]) {
				p.pos++
			}
			if p.pos == start {
				return fmt.Errorf("empty language tag")
			}
			quad.language = string(p.data[start:p.pos])
		} else if p.pos+1 < len(p.data) && p.data[p.pos] == '^' && p.data[p.pos+1] == '^' {
			// datatype
			p.pos += 2 // skip '^^'
			if p.pos >= len(p.data) || p.data[p.pos] != '<' {
				return fmt.Errorf("expected IRI after ^^")
			}
			iri, err := p.parseIRI()
			if err != nil {
				return err
			}
			quad.datatype = iri
		}
	}

	return nil
}

func (p *quadParser) parseGraphAndTerminator(quad *parsedQuad) error {
	if p.pos >= len(p.data) {
		return fmt.Errorf("unexpected end of input")
	}

	// check for graph name or just terminator
	if p.data[p.pos] == '.' {
		p.pos++
		return nil
	}

	// parse graph (IRI or blank node)
	if p.data[p.pos] == '<' {
		iri, err := p.parseIRI()
		if err != nil {
			return err
		}
		quad.graphIRI = iri
	} else if p.pos+1 < len(p.data) && p.data[p.pos] == '_' && p.data[p.pos+1] == ':' {
		bnode, err := p.parseBlankNode()
		if err != nil {
			return err
		}
		quad.graphBNode = bnode
	} else {
		return fmt.Errorf("invalid graph name")
	}

	// skip whitespace before terminator
	p.skipWhitespace()

	// expect terminator '.'
	if p.pos >= len(p.data) || p.data[p.pos] != '.' {
		return fmt.Errorf("expected '.' terminator")
	}
	p.pos++

	return nil
}

func isBlankNodeStartChar(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
		(c >= '0' && c <= '9') || c == '_' || c >= 0xC0
}

func isBlankNodeChar(c byte) bool {
	return isBlankNodeStartChar(c) || c == '-' || c == '.' || c == 0xB7
}

func isLanguageChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') || c == '-'
}

// ParseNQuadsFrom parses RDF in the form of N-Quads from io.Reader, []byte or string.
func ParseNQuadsFrom(o interface{}) (*RDFDataset, error) {

	// build RDF dataset
	dataset := NewRDFDataset()

	// maintain a set of triples for each graph to check for duplicates
	triplesByGraph := make(map[string]map[Quad]struct{})
	dataset.parsedWithoutDuplicates = true // the following code ensures that no duplicate quads are added

	scanner, err := newScannerFor(o)
	if err != nil {
		return nil, err
	}

	// scan N-Quad input lines
	lineNumber := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		lineNumber++

		// skip empty lines
		if isEmpty(line) {
			continue
		}

		// parse quad using custom parser
		quad, err := parseQuadLine(line)
		if err != nil {
			return nil, NewJsonLdError(SyntaxError, fmt.Errorf("error while parsing N-Quads; invalid quad. line: %d: %v", lineNumber, err))
		}

		// get subject
		var subject Node
		if quad.subjectIRI != "" {
			subject = NewIRI(unescape(quad.subjectIRI))
		} else {
			subject = NewBlankNode(unescape(quad.subjectBNode))
		}

		// get predicate
		predicate := NewIRI(unescape(quad.predicate))

		// get object
		var object Node
		if quad.objectIRI != "" {
			object = NewIRI(unescape(quad.objectIRI))
		} else if quad.objectBNode != "" {
			object = NewBlankNode(unescape(quad.objectBNode))
		} else {
			language := unescape(quad.language)
			var datatype string
			if quad.datatype != "" {
				datatype = unescape(quad.datatype)
			} else if quad.language != "" {
				datatype = RDFLangString
			} else {
				datatype = XSDString
			}
			unescaped := unescape(quad.literal)
			object = NewLiteral(unescaped, datatype, language)
		}

		// get graph name ('@default' is used for the default graph)
		name := "@default"
		if quad.graphIRI != "" {
			name = unescape(quad.graphIRI)
		} else if quad.graphBNode != "" {
			name = unescape(quad.graphBNode)
		}

		triple := NewQuad(subject, predicate, object, name)

		// initialise graph in dataset
		triples, present := dataset.Graphs[name]
		if triplesByGraph[name] == nil {
			triplesByGraph[name] = make(map[Quad]struct{})
		}

		if !present {
			dataset.Graphs[name] = []*Quad{triple}
		} else {
			// add triple if unique to its graph
			if _, hasTriple := triplesByGraph[name][*triple]; !hasTriple {
				dataset.Graphs[name] = append(triples, triple)
			}
		}
		triplesByGraph[name][*triple] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return nil, NewJsonLdError(IOError, err)
	}

	return dataset, nil
}

// ParseNQuads parses RDF in the form of N-Quads.
func ParseNQuads(input string) (*RDFDataset, error) {
	return ParseNQuadsFrom(input)
}

func isEmpty(line []byte) bool {
	for _, b := range line {
		if b != ' ' && b != '\t' {
			return false
		}
	}
	return true
}

package suggest

import (
	"bytes"
	"go/scanner"
	"go/token"
)

type token_iterator struct {
	tokens      []token_item
	token_index int
}

type token_item struct {
	off int
	tok token.Token
	lit string
}

func (i token_item) literal() string {
	if i.tok.IsLiteral() {
		return i.lit
	} else {
		return i.tok.String()
	}
	return ""
}

func new_token_iterator(src []byte, cursor int) token_iterator {
	tokens := make([]token_item, 0, 1000)
	var s scanner.Scanner
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	s.Init(file, src, nil, 0)
	for {
		pos, tok, lit := s.Scan()
		off := fset.Position(pos).Offset
		if tok == token.EOF || cursor <= off {
			break
		}
		tokens = append(tokens, token_item{
			off: off,
			tok: tok,
			lit: lit,
		})
	}
	return token_iterator{
		tokens:      tokens,
		token_index: len(tokens) - 1,
	}
}

func (this *token_iterator) token() token_item {
	return this.tokens[this.token_index]
}

func (this *token_iterator) go_back() bool {
	if this.token_index <= 0 {
		return false
	}
	this.token_index--
	return true
}

var bracket_pairs_map = map[token.Token]token.Token{
	token.RPAREN: token.LPAREN,
	token.RBRACK: token.LBRACK,
	token.RBRACE: token.LBRACE,
}

func (ti *token_iterator) skip_to_left(left, right token.Token) bool {
	if ti.token().tok == left {
		return true
	}
	balance := 1
	for balance != 0 {
		if !ti.go_back() {
			return false
		}
		switch ti.token().tok {
		case right:
			balance++
		case left:
			balance--
		}
	}
	return true
}

// when the cursor is at the ')' or ']' or '}', move the cursor to an opposite
// bracket pair, this functions takes nested bracket pairs into account
func (this *token_iterator) skip_to_balanced_pair() bool {
	right := this.token().tok
	left := bracket_pairs_map[right]
	return this.skip_to_left(left, right)
}

// Move the cursor to the open brace of the current block, taking nested blocks
// into account.
func (this *token_iterator) skip_to_left_curly() bool {
	return this.skip_to_left(token.LBRACE, token.RBRACE)
}

// Extract the type expression right before the enclosing curly bracket block.
// Examples (# - the cursor):
//   &lib.Struct{Whatever: 1, Hel#} // returns "lib.Struct"
//   X{#}                           // returns X
// The idea is that we check if this type expression is a type and it is, we
// can apply special filtering for autocompletion results.
// Sadly, this doesn't cover anonymous structs.
func (ti *token_iterator) extract_struct_type() (res string) {
	defer func() {
		if res != "" {
			// fmt.Println("Extracted struct type:", res)
		}
	}()
	if !ti.skip_to_left_curly() {
		return ""
	}
	if !ti.go_back() {
		return ""
	}
	if ti.token().tok != token.IDENT {
		return ""
	}
	b := ti.token().literal()
	if !ti.go_back() {
		return b
	}
	if ti.token().tok != token.PERIOD {
		return b
	}
	if !ti.go_back() {
		return b
	}
	if ti.token().tok != token.IDENT {
		return b
	}
	return ti.token().literal() + "." + b
}

// Starting from the token under the cursor move back and extract something
// that resembles a valid Go primary expression. Examples of primary expressions
// from Go spec:
//   x
//   2
//   (s + ".txt")
//   f(3.1415, true)
//   Point{1, 2}
//   m["foo"]
//   s[i : j + 1]
//   obj.color
//   f.p[i].x()
//
// As you can see we can move through all of them using balanced bracket
// matching and applying simple rules
// E.g.
//   Point{1, 2}.m["foo"].s[i : j + 1].MethodCall(a, func(a, b int) int { return a + b }).
// Can be seen as:
//   Point{    }.m[     ].s[         ].MethodCall(                                      ).
// Which boils the rules down to these connected via dots:
//   ident
//   ident[]
//   ident{}
//   ident()
// Of course there are also slightly more complicated rules for brackets:
//   ident{}.ident()[5][4](), etc.
func (this *token_iterator) extract_go_expr() string {
	orig := this.token_index

	// Contains the type of the previously scanned token (initialized with
	// the token right under the cursor). This is the token to the *right* of
	// the current one.
	prev := this.token().tok
loop:
	for {
		if !this.go_back() {
			return token_items_to_string(this.tokens[:orig])
		}
		switch this.token().tok {
		case token.PERIOD:
			// If the '.' is not followed by IDENT, it's invalid.
			if prev != token.IDENT {
				break loop
			}
		case token.IDENT:
			// Valid tokens after IDENT are '.', '[', '{' and '('.
			switch prev {
			case token.PERIOD, token.LBRACK, token.LBRACE, token.LPAREN:
				// all ok
			default:
				break loop
			}
		case token.RBRACE:
			// This one can only be a part of type initialization, like:
			//   Dummy{}.Hello()
			// It is valid Go if Hello method is defined on a non-pointer receiver.
			if prev != token.PERIOD {
				break loop
			}
			this.skip_to_balanced_pair()
		case token.RPAREN, token.RBRACK:
			// After ']' and ')' their opening counterparts are valid '[', '(',
			// as well as the dot.
			switch prev {
			case token.PERIOD, token.LBRACK, token.LPAREN:
				// all ok
			default:
				break loop
			}
			this.skip_to_balanced_pair()
		default:
			break loop
		}
		prev = this.token().tok
	}
	return token_items_to_string(this.tokens[this.token_index+1 : orig])
}

// Given a slice of token_item, reassembles them into the original literal
// expression.
func token_items_to_string(tokens []token_item) string {
	var buf bytes.Buffer
	for _, t := range tokens {
		buf.WriteString(t.literal())
	}
	return buf.String()
}

type cursorContext int

const (
	unknownContext cursorContext = iota
	importContext
	selectContext
	compositeLiteralContext
)

func deduce_cursor_context_helper(file []byte, cursor int) (cursorContext, string, string) {
	iter := new_token_iterator(file, cursor)
	if len(iter.tokens) == 0 {
		return unknownContext, "", ""
	}

	// figure out what is just before the cursor
	switch tok := iter.token(); tok.tok {
	case token.STRING:
		// make sure cursor is inside the string
		s := tok.literal()
		if len(s) > 1 && s[len(s)-1] == '"' && tok.off+len(s) <= cursor {
			return unknownContext, "", ""
		}
		// now figure out if inside an import declaration
		var ptok = token.STRING
		for iter.go_back() {
			itok := iter.token().tok
			switch itok {
			case token.STRING:
				switch ptok {
				case token.SEMICOLON, token.IDENT, token.PERIOD:
				default:
					return unknownContext, "", ""
				}
			case token.LPAREN, token.SEMICOLON:
				switch ptok {
				case token.STRING, token.IDENT, token.PERIOD:
				default:
					return unknownContext, "", ""
				}
			case token.IDENT, token.PERIOD:
				switch ptok {
				case token.STRING:
				default:
					return unknownContext, "", ""
				}
			case token.IMPORT:
				switch ptok {
				case token.STRING, token.IDENT, token.PERIOD, token.LPAREN:
					path_len := cursor - tok.off
					path := s[1:path_len]
					return importContext, "", path
				default:
					return unknownContext, "", ""
				}
			default:
				return unknownContext, "", ""
			}
			ptok = itok
		}
	case token.PERIOD:
		// we're '<whatever>.'
		// figure out decl, Partial is ""
		return selectContext, iter.extract_go_expr(), ""
	case token.IDENT, token.TYPE, token.CONST, token.VAR, token.FUNC, token.PACKAGE:
		// we're '<whatever>.<ident>'
		// parse <ident> as Partial and figure out decl
		var partial string
		if tok.tok == token.IDENT {
			// Calculate the offset of the cursor position within the identifier.
			// For instance, if we are 'ab#c', we want partial_len = 2 and partial = ab.
			partial_len := cursor - tok.off

			// If it happens that the cursor is past the end of the literal,
			// means there is a space between the literal and the cursor, think
			// of it as no context, because that's what it really is.
			if partial_len > len(tok.literal()) {
				return unknownContext, "", ""
			}
			partial = tok.literal()[0:partial_len]
		} else {
			// Do not try to truncate if it is not an identifier.
			partial = tok.literal()
		}

		iter.go_back()
		switch iter.token().tok {
		case token.PERIOD:
			return selectContext, iter.extract_go_expr(), partial
		case token.COMMA, token.LBRACE:
			// This can happen for struct fields:
			// &Struct{Hello: 1, Wor#} // (# - the cursor)
			// Let's try to find the struct type
			return compositeLiteralContext, iter.extract_struct_type(), partial
		default:
			return unknownContext, "", partial
		}
	case token.COMMA, token.LBRACE:
		// Try to parse the current expression as a structure initialization.
		return compositeLiteralContext, iter.extract_struct_type(), ""
	}

	return unknownContext, "", ""
}

package cmd

import (
	"errors"
	"regexp"
	"strings"
)

// VALUES data is checked incrementally. Only a short atom is retained; quoted
// strings and arbitrarily long hexadecimal/bit data are validated in place.
var dumpNumber = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

const (
	valuesHead = iota
	valuesColumn
	valuesColumnSep
	valuesKeyword
	valuesTuple
	valuesLiteral
	valuesLiteralSep
	valuesTupleSep
	valuesIntroduced
)

type dumpValues struct {
	state       int
	word        []byte
	radix       byte
	digits      bool
	quote       byte
	quotedRadix byte
}

func literalError() error {
	return errors.New("INSERT/REPLACE must contain only literal VALUES tuples")
}

func (v *dumpValues) token(t sqlToken) error {
	if t.kind == '\'' || t.kind == '"' || t.kind == '`' {
		if err := v.startQuote(t.kind); err != nil {
			return err
		}
		if err := v.quoteText([]byte(t.text)); err != nil {
			return err
		}
		return v.endQuote()
	}
	for _, b := range []byte(t.text) {
		if err := v.codeByte(b); err != nil {
			return err
		}
	}
	return v.space()
}

func (v *dumpValues) codeByte(b byte) error {
	if isSpace(b) {
		return v.space()
	}
	if isWordByte(b) || b == '.' || b == '+' || b == '-' {
		if v.radix != 0 {
			if !radixDigit(b, v.radix) {
				return literalError()
			}
			v.digits = true
			return nil
		}
		// A 0x/0b literal can be much larger than a statement head.
		if v.state == valuesLiteral && len(v.word) == 1 && v.word[0] == '0' && (b == 'x' || b == 'b') {
			v.radix = b
			v.word = v.word[:0]
			return nil
		}
		if len(v.word) >= 128 {
			return literalError()
		}
		v.word = append(v.word, b)
		return nil
	}
	if err := v.space(); err != nil {
		return err
	}
	switch {
	case b == '(' && v.state == valuesHead:
		v.state = valuesColumn
	case b == '(' && v.state == valuesTuple:
		v.state = valuesLiteral
	case b == ',' && v.state == valuesColumnSep:
		v.state = valuesColumn
	case b == ')' && v.state == valuesColumnSep:
		v.state = valuesKeyword
	case b == ',' && v.state == valuesLiteralSep:
		v.state = valuesLiteral
	case b == ')' && v.state == valuesLiteralSep:
		v.state = valuesTupleSep
	case b == ',' && v.state == valuesTupleSep:
		v.state = valuesTuple
	default:
		return literalError()
	}
	return nil
}

func (v *dumpValues) space() error {
	if v.radix != 0 {
		if !v.digits {
			return literalError()
		}
		v.radix, v.digits = 0, false
		v.state = valuesLiteralSep
		return nil
	}
	if len(v.word) == 0 {
		return nil
	}
	w := string(v.word)
	v.word = v.word[:0]
	switch v.state {
	case valuesHead, valuesKeyword:
		if !strings.EqualFold(w, "VALUES") {
			return literalError()
		}
		v.state = valuesTuple
	case valuesColumn:
		for _, b := range []byte(w) {
			if !isWordByte(b) {
				return literalError()
			}
		}
		v.state = valuesColumnSep
	case valuesLiteral:
		switch {
		case w == "+" || w == "-":
			// Whitespace between a unary sign and a numeric literal is harmless.
			v.word = append(v.word, w...)
		case strings.EqualFold(w, "NULL"), dumpNumber.MatchString(w):
			v.state = valuesLiteralSep
		case strings.EqualFold(w, "X"), strings.EqualFold(w, "B"), strings.EqualFold(w, "N"):
			v.state = valuesIntroduced
			if strings.EqualFold(w, "X") {
				v.quotedRadix = 'x'
			}
			if strings.EqualFold(w, "B") {
				v.quotedRadix = 'b'
			}
		case strings.HasPrefix(w, "_") && len(w) > 1:
			// Charset introducers are words, never expressions. Unsafe multibyte
			// charsets are refused even though session decoding remains unchanged.
			if unsafeCharset(w[1:]) {
				return literalError()
			}
			for _, b := range []byte(w[1:]) {
				if !isWordByte(b) || b >= 0x80 {
					return literalError()
				}
			}
			v.state = valuesIntroduced
		default:
			return literalError()
		}
	default:
		return literalError()
	}
	return nil
}

func (v *dumpValues) startQuote(q byte) error {
	if err := v.space(); err != nil {
		return err
	}
	switch {
	case v.state == valuesColumn && q == '`':
	case v.state == valuesLiteral && (q == '\'' || q == '"'):
	case v.state == valuesIntroduced && (q == '\'' || q == '"') && (v.quotedRadix == 0 || q == '\''):
	default:
		return literalError()
	}
	v.quote = q
	return nil
}

func (v *dumpValues) quoteText(b []byte) error {
	if v.quotedRadix != 0 {
		for _, c := range b {
			if !radixDigit(c, v.quotedRadix) {
				return literalError()
			}
		}
	}
	return nil
}

func (v *dumpValues) endQuote() error {
	if v.quote == 0 {
		return literalError()
	}
	if v.state == valuesColumn {
		v.state = valuesColumnSep
	} else {
		v.state = valuesLiteralSep
	}
	v.quote, v.quotedRadix = 0, 0
	return nil
}

func (v *dumpValues) end() error {
	if err := v.space(); err != nil {
		return err
	}
	if v.state != valuesTupleSep || v.quote != 0 || len(v.word) != 0 {
		return literalError()
	}
	return nil
}

func radixDigit(b, radix byte) bool {
	if radix == 'b' {
		return b == '0' || b == '1'
	}
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}

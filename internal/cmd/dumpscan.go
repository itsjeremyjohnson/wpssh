package cmd

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
)

// scanDump reads a SQL dump to EOF and refuses it unless the mysql client
// would only send the server SQL statements of the kinds mysqldump,
// mariadb-dump and `wp db export` write. database is the database the import
// writes to. Memory use does not grow with the dump's size or line length.
//
// The scanner reads the input the way the mysql client does: ', " and `
// quotes with backslash escapes, # and "-- " comments, lines starting with #
// or --, /* */ comments, and /*!NNNNN */, /*M!NNNNNN */ and /*+ */ comments,
// whose contents the client and server treat as SQL. It refuses:
//
//   - a backslash outside quotes and comments, which the client reads as a
//     one-letter command (\! runs a shell command, \. runs a file, \r and \u
//     switch database, \T writes a file, \C changes how later bytes are read).
//     The one exception is \- , MariaDB's switch that turns client commands
//     off, which mariadb-dump writes on its first line;
//   - a line whose first word is one of clientLineCommands, or use naming a
//     database other than the target;
//   - a statement that checkHead does not allow. Its allowlist also excludes
//     every other long-form client command (quit, prompt, ...), which the
//     client recognizes at the start of a statement;
//   - a SET that could turn on NO_BACKSLASH_ESCAPES or switch to a character
//     set in which a backslash byte can end a character, since either would
//     make the client end quotes where this scanner does not;
//   - OUTFILE or DUMPFILE outside quotes, which write files on the server.
//
// DELIMITER at the start of a line between statements is allowed;
// mysqldump writes DELIMITER ;; around triggers and routines.
func scanDump(r io.Reader, database string) error {
	s := &dumpScanner{
		r:     bufio.NewReaderSize(r, 64<<10),
		delim: []byte(";"),
		line:  1,
		stmt:  stmtCheck{database: database},
	}
	if err := s.run(); err != nil {
		return fmt.Errorf("refusing dump: line %d: %w", s.line, err)
	}
	return nil
}

// clientLineCommands are the mysql client's long-form commands refused as
// the first word of any line, with what each does.
var clientLineCommands = map[string]string{
	"system":  "runs a shell command on the server",
	"source":  "runs a file from the server",
	"connect": "reconnects, possibly to another database",
	"tee":     "writes output to a file on the server",
	"pager":   "pipes output through a shell command on the server",
	"edit":    "runs an editor on the server",
	"charset": "changes how the client reads the rest of the dump",
}

// unsafeCharsets are character sets in which byte 0x5c (backslash) can be
// the second byte of a character.
var unsafeCharsets = []string{"BIG5", "CP932", "GBK", "GB18030", "SJIS"}

type scanState int

const (
	stCode scanState = iota
	stQuote
	stComment
)

type dumpScanner struct {
	r     *bufio.Reader
	delim []byte
	line  int64

	state scanState
	quote byte
	// cond is set inside /*! */, /*M! */ and /*+ */. dead is set inside
	// /*M!999999 */, which no server version runs.
	cond, dead bool

	stmt stmtCheck
	// word is the code word being read, for the OUTFILE check.
	word     []byte
	wordLong bool
}

func (s *dumpScanner) run() error {
	for {
		consumed, eof := false, false
		var err error
		if s.state == stCode {
			consumed, eof, err = s.lineStart()
		}
		if err == nil && !consumed {
			eof, err = s.restOfLine()
		}
		if err == nil {
			err = s.stmt.err
		}
		if err != nil {
			return err
		}
		if eof {
			if err := s.endWord(); err != nil {
				return err
			}
			// The client sends a statement left open at EOF.
			return s.stmt.end()
		}
		s.line++
	}
}

// lineStart handles the start of a line read in code state. It consumes the
// whole line, newline included, when the line is a comment or an allowed
// client command; otherwise it consumes only leading whitespace.
func (s *dumpScanner) lineStart() (consumed, eof bool, err error) {
	if head, _ := s.r.Peek(2); len(head) > 0 && (head[0] == '#' || bytes.HasPrefix(head, []byte("--"))) {
		// The client drops a line starting with # or -- whole.
		eof, err = s.skipLine()
		return true, eof, err
	}
	for {
		b, err := s.r.Peek(1)
		if err != nil || !isLineSpace(b[0]) {
			break
		}
		_, _ = s.r.Discard(1)
	}
	peek, _ := s.r.Peek(33)
	n := 0
	for n < len(peek) && isWordByte(peek[n]) {
		n++
	}
	if n == 0 || n > 32 {
		return false, false, nil
	}
	word := strings.ToLower(string(peek[:n]))
	if why := clientLineCommands[word]; why != "" {
		return false, false, fmt.Errorf("mysql client command %q %s", word, why)
	}
	switch {
	case word == "use":
	case word == "delimiter" && n < len(peek) && isLineSpace(peek[n]):
	default:
		return false, false, nil
	}
	_, _ = s.r.Discard(n)
	arg, eof, err := s.readLine(1024)
	if err != nil {
		return true, eof, err
	}
	if word == "delimiter" {
		return true, eof, s.setDelimiter(arg)
	}
	return true, eof, checkUse(arg, s.delim, s.stmt.database)
}

// skipLine discards through the next newline and reports EOF.
func (s *dumpScanner) skipLine() (bool, error) {
	for {
		_, err := s.r.ReadSlice('\n')
		switch {
		case err == nil:
			return false, nil
		case errors.Is(err, bufio.ErrBufferFull):
		case errors.Is(err, io.EOF):
			return true, nil
		default:
			return false, err
		}
	}
}

// readLine returns the rest of the line, consuming the newline, and refuses
// lines longer than maxLen.
func (s *dumpScanner) readLine(maxLen int) (string, bool, error) {
	var b []byte
	for {
		c, err := s.r.ReadByte()
		if errors.Is(err, io.EOF) {
			return string(b), true, nil
		}
		if err != nil {
			return "", false, err
		}
		if c == '\n' {
			return string(b), false, nil
		}
		if len(b) == maxLen {
			return "", false, errors.New("client command line too long")
		}
		b = append(b, c)
	}
}

func (s *dumpScanner) setDelimiter(arg string) error {
	if s.stmt.started {
		return errors.New("DELIMITER inside a statement")
	}
	fields := strings.Fields(arg)
	if len(fields) != 1 || len(fields[0]) > 16 {
		return fmt.Errorf("unsupported DELIMITER %q", arg)
	}
	// A delimiter of plain punctuation cannot open a quote or comment, so a
	// partial match is ordinary code.
	for _, c := range []byte(fields[0]) {
		if isSpace(c) || isWordByte(c) || strings.IndexByte("'\"`\\#-/*@.", c) >= 0 {
			return fmt.Errorf("unsupported DELIMITER %q: use punctuation such as ;; or $$", fields[0])
		}
	}
	s.delim = []byte(fields[0])
	return nil
}

// checkUse allows the client's use command only for the target database.
func checkUse(arg string, delim []byte, database string) error {
	name := strings.TrimSpace(arg)
	name = strings.TrimSpace(strings.TrimSuffix(name, string(delim)))
	name = strings.TrimSpace(strings.TrimSuffix(name, ";"))
	if len(name) >= 2 && strings.IndexByte("`'\"", name[0]) >= 0 && name[len(name)-1] == name[0] {
		name = name[1 : len(name)-1]
	}
	if database == "" || name != database {
		return fmt.Errorf("USE %q: the import may only write to the site database %q", name, database)
	}
	return nil
}

// restOfLine scans through the newline that ends the line and reports EOF.
func (s *dumpScanner) restOfLine() (bool, error) {
	for s.stmt.err == nil {
		if s.state == stQuote {
			s.skipQuoted()
		}
		c, err := s.r.ReadByte()
		if errors.Is(err, io.EOF) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if c == '\n' {
			if s.state == stCode {
				s.stmt.space()
				return false, s.endWord()
			}
			return false, nil
		}
		switch s.state {
		case stComment:
			if c == '*' && s.next('/') {
				s.state = stCode
			}
		case stQuote:
			if err := s.quoted(c); err != nil {
				return false, err
			}
		case stCode:
			if err := s.code(c); err != nil {
				return false, err
			}
		}
	}
	return false, nil
}

// skipQuoted discards buffered bytes inside a quote up to the next byte that
// could end the quote or the line.
func (s *dumpScanner) skipQuoted() {
	buf, _ := s.r.Peek(s.r.Buffered())
	n := bytes.IndexAny(buf, string([]byte{s.quote, '\\', '\n'}))
	if n < 0 {
		n = len(buf)
	}
	s.stmt.quoteText(buf[:n])
	_, _ = s.r.Discard(n)
}

func (s *dumpScanner) quoted(c byte) error {
	switch c {
	case '\\':
		// The client keeps the escaped byte, even a newline, in the quote.
		e, err := s.r.ReadByte()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if e == '\n' {
			s.line++
		}
		s.stmt.quoteText([]byte{'\\', e})
	case s.quote:
		s.state = stCode
		s.stmt.endQuote()
	default:
		s.stmt.quoteText([]byte{c})
	}
	return nil
}

// next consumes the next byte if it is c.
func (s *dumpScanner) next(c byte) bool {
	if b, err := s.r.Peek(1); err == nil && b[0] == c {
		_, _ = s.r.Discard(1)
		return true
	}
	return false
}

// code handles one byte other than a newline read in code state.
func (s *dumpScanner) code(c byte) error {
	if !isWordByte(c) {
		if err := s.endWord(); err != nil {
			return err
		}
	}
	if c == s.delim[0] {
		if rest, _ := s.r.Peek(len(s.delim) - 1); bytes.Equal(rest, s.delim[1:]) {
			_, _ = s.r.Discard(len(s.delim) - 1)
			return s.stmt.end()
		}
	}
	switch {
	case c == '\'' || c == '"' || c == '`':
		s.state, s.quote = stQuote, c
		s.stmt.startQuote(c, s.dead)
		return nil
	case c == '\\':
		if s.next('-') {
			return nil
		}
		cmd, _ := s.r.Peek(1)
		return fmt.Errorf("mysql client command %q outside quotes: client commands are not SQL and run on the server", `\`+string(cmd))
	case c == '#':
		s.lineComment()
		return nil
	case c == '-' && s.dashComment():
		s.lineComment()
		return nil
	case c == '/' && s.next('*'):
		return s.openComment()
	case c == '*' && s.cond && s.next('/'):
		s.cond, s.dead = false, false
		s.stmt.space()
		return nil
	}
	if isWordByte(c) {
		if len(s.word) < 16 {
			s.word = append(s.word, c)
		} else {
			s.wordLong = true
		}
	}
	if !s.dead {
		s.stmt.codeByte(c)
	}
	return nil
}

// dashComment reports whether the '-' just read starts a "-- " comment, and
// consumes the second dash if so.
func (s *dumpScanner) dashComment() bool {
	b, _ := s.r.Peek(2)
	if len(b) == 0 || b[0] != '-' || len(b) == 2 && !isSpace(b[1]) {
		return false
	}
	_, _ = s.r.Discard(1)
	return true
}

// lineComment discards the rest of the line, leaving the newline unread.
func (s *dumpScanner) lineComment() {
	s.stmt.space()
	for {
		buf, _ := s.r.Peek(s.r.Buffered())
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			_, _ = s.r.Discard(i)
			return
		}
		_, _ = s.r.Discard(len(buf))
		if _, err := s.r.Peek(1); err != nil {
			return
		}
	}
}

// openComment handles the bytes after "/*".
func (s *dumpScanner) openComment() error {
	s.stmt.space()
	b, _ := s.r.Peek(2)
	switch {
	case len(b) > 0 && b[0] == '!':
		_, _ = s.r.Discard(1)
	case len(b) > 1 && b[0] == 'M' && b[1] == '!':
		_, _ = s.r.Discard(2)
	case len(b) > 0 && b[0] == '+':
		_, _ = s.r.Discard(1)
		s.cond = true
		return nil
	default:
		s.state = stComment
		return nil
	}
	if s.cond {
		return errors.New("nested /*! comment")
	}
	digits, _ := s.r.Peek(6)
	n := 0
	for n < len(digits) && digits[n] >= '0' && digits[n] <= '9' {
		n++
	}
	_, _ = s.r.Discard(n)
	s.cond = true
	s.dead = string(digits[:n]) == "999999"
	return nil
}

// endWord checks the code word just finished.
func (s *dumpScanner) endWord() error {
	w, long := s.word, s.wordLong
	s.word, s.wordLong = s.word[:0], false
	if long || len(w) < 7 {
		return nil
	}
	for _, kw := range []string{"OUTFILE", "DUMPFILE"} {
		if strings.EqualFold(string(w), kw) {
			return fmt.Errorf("%s outside quotes writes a file on the database server", kw)
		}
	}
	return nil
}

func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '$' || c >= 0x80
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

func isLineSpace(c byte) bool {
	return c != '\n' && isSpace(c)
}

// sqlToken is one token of a statement: a bare word (with @ and . kept, so
// @@SESSION.sql_mode is one word), a quoted string or identifier, or one
// punctuation byte.
type sqlToken struct {
	kind byte // 'w' word, 'p' punctuation, or the quote byte
	text string
}

func (t sqlToken) is(word string) bool {
	return t.kind == 'w' && strings.EqualFold(t.text, word)
}

// maxStatementHead bounds the bytes kept of one statement: the head of most
// statements, and SET statements whole.
const maxStatementHead = 64 << 10

// maxHeadTokens bounds the tokens read to find what a CREATE, DROP or ALTER
// creates, drops or alters.
const maxHeadTokens = 48

// stmtCheck collects the current statement until checkHead accepts it, and
// keeps SET statements whole. err is set when it refuses a statement.
type stmtCheck struct {
	database string
	started  bool
	accepted bool
	tokens   []sqlToken
	size     int
	cur      []byte
	curKind  byte
	err      error
	// savedModes are user variables last assigned from @@sql_mode.
	savedModes map[string]bool
}

func (c *stmtCheck) codeByte(b byte) {
	if isSpace(b) {
		c.space()
		return
	}
	c.started = true
	if c.accepted {
		return
	}
	word := isWordByte(b) || b == '@' || b == '.'
	if c.curKind == 'w' && word {
		c.cur = append(c.cur, b)
		return
	}
	c.flush()
	if word {
		c.curKind, c.cur = 'w', append(c.cur[:0], b)
		return
	}
	c.push(sqlToken{kind: 'p', text: string(b)})
}

func (c *stmtCheck) space() { c.flush() }

func (c *stmtCheck) startQuote(q byte, dead bool) {
	c.flush()
	if dead {
		return
	}
	c.started = true
	if !c.accepted {
		c.curKind, c.cur = q, c.cur[:0]
	}
}

func (c *stmtCheck) quoteText(b []byte) {
	if c.curKind == 0 || c.curKind == 'w' {
		return
	}
	if c.size+len(c.cur)+len(b) > maxStatementHead {
		c.refuse(errors.New("statement too long to check"))
		return
	}
	c.cur = append(c.cur, b...)
}

func (c *stmtCheck) endQuote() { c.flush() }

func (c *stmtCheck) flush() {
	if c.curKind == 0 {
		return
	}
	t := sqlToken{kind: c.curKind, text: string(c.cur)}
	c.curKind, c.cur = 0, c.cur[:0]
	c.push(t)
}

func (c *stmtCheck) push(t sqlToken) {
	if c.accepted || c.err != nil {
		return
	}
	c.tokens = append(c.tokens, t)
	c.size += len(t.text)
	if c.size > maxStatementHead {
		c.refuse(errors.New("statement too long to check"))
		return
	}
	ok, err := c.checkHead(false)
	switch {
	case err != nil:
		c.refuse(err)
	case ok:
		c.accepted = true
		c.tokens = c.tokens[:0]
	case len(c.tokens) > maxHeadTokens && !c.tokens[0].is("SET"):
		c.refuse(fmt.Errorf("statement %s is not one mysqldump writes", describe(c.tokens, 4)))
	}
}

func (c *stmtCheck) refuse(err error) {
	if c.err == nil {
		c.err = err
	}
}

// end checks the statement that just ended and resets for the next one.
func (c *stmtCheck) end() error {
	c.flush()
	if c.err == nil && !c.accepted && len(c.tokens) > 0 {
		if ok, err := c.checkHead(true); err != nil || !ok {
			c.refuse(err)
		}
	}
	*c = stmtCheck{database: c.database, savedModes: c.savedModes, tokens: c.tokens[:0], cur: c.cur[:0], err: c.err}
	return c.err
}

// checkHead reports whether the statement so far is allowed (true), refused
// (an error), or needs more tokens (false, nil). With final set, the
// statement is complete.
func (c *stmtCheck) checkHead(final bool) (bool, error) {
	t := c.tokens
	more := func() (bool, error) {
		if final {
			return false, fmt.Errorf("statement %s is not one mysqldump writes", describe(t, 4))
		}
		return false, nil
	}
	switch {
	case t[0].is("INSERT"), t[0].is("REPLACE"), t[0].is("COMMIT"):
		return true, nil
	case t[0].is("LOCK"), t[0].is("UNLOCK"):
		if len(t) < 2 {
			return more()
		}
		if t[1].is("TABLES") || t[1].is("TABLE") {
			return true, nil
		}
	case t[0].is("SET"):
		if !final {
			return false, nil
		}
		return true, c.checkSet(t[1:])
	case t[0].is("USE"):
		if len(t) < 2 {
			return more()
		}
		return true, c.checkName("USE", t[1])
	case t[0].is("CREATE"), t[0].is("DROP"), t[0].is("ALTER"):
		return c.checkDDL(final)
	}
	return false, fmt.Errorf("statement %s is not one mysqldump writes", describe(t, 3))
}

// ddlModifiers are words that may come between CREATE, DROP or ALTER and the
// kind of object.
var ddlModifiers = map[string]bool{
	"OR": true, "REPLACE": true, "ALGORITHM": true, "UNDEFINED": true, "MERGE": true,
	"TEMPTABLE": true, "DEFINER": true, "CURRENT_USER": true, "SQL": true, "SECURITY": true,
	"INVOKER": true, "TEMPORARY": true, "AGGREGATE": true, "ONLINE": true, "OFFLINE": true, "IGNORE": true,
}

// ddlObjects are the objects mysqldump creates and drops.
var ddlObjects = map[string]bool{
	"TABLE": true, "VIEW": true, "TRIGGER": true, "PROCEDURE": true, "FUNCTION": true, "EVENT": true,
}

func (c *stmtCheck) checkDDL(final bool) (bool, error) {
	verb := strings.ToUpper(c.tokens[0].text)
	for i, t := range c.tokens[1:] {
		kind := strings.ToUpper(t.text)
		if t.kind != 'w' || ddlModifiers[kind] || strings.Contains(kind, "@") {
			continue
		}
		switch {
		case ddlObjects[kind] && (verb != "ALTER" || kind == "TABLE"):
			return true, nil
		case (kind == "DATABASE" || kind == "SCHEMA") && verb != "DROP":
			return c.checkDatabaseName(verb, c.tokens[i+2:], final)
		}
		return false, fmt.Errorf("%s %s is not a statement mysqldump writes", verb, kind)
	}
	if final {
		return false, fmt.Errorf("statement %s is not one mysqldump writes", describe(c.tokens, 4))
	}
	return false, nil
}

// checkDatabaseName allows CREATE or ALTER DATABASE only for the target
// database, or ALTER DATABASE with no name, which alters the current one.
func (c *stmtCheck) checkDatabaseName(verb string, rest []sqlToken, final bool) (bool, error) {
	for _, t := range rest {
		switch {
		case t.is("IF"), t.is("NOT"), t.is("EXISTS"):
			continue
		case verb == "ALTER" && (t.is("CHARACTER") || t.is("CHARSET") || t.is("DEFAULT") || t.is("COLLATE")):
			return true, nil
		}
		return true, c.checkName(verb+" DATABASE", t)
	}
	if final {
		return false, fmt.Errorf("%s DATABASE without a name", verb)
	}
	return false, nil
}

func (c *stmtCheck) checkName(what string, t sqlToken) error {
	if c.database != "" && (t.kind == 'w' || t.kind == '`' || t.kind == '"' || t.kind == '\'') && t.text == c.database {
		return nil
	}
	return fmt.Errorf("%s %q: the import may only write to the site database %q", what, t.text, c.database)
}

// checkSet refuses SET assignments that could make the client end quotes
// where this scanner does not.
func (c *stmtCheck) checkSet(t []sqlToken) error {
	if len(t) > 0 && t[0].is("STATEMENT") {
		return errors.New("SET STATEMENT is not one mysqldump writes")
	}
	for _, tok := range t {
		up := strings.ToUpper(tok.text)
		for _, cs := range unsafeCharsets {
			if tok.kind != 'p' && (up == cs || strings.HasPrefix(up, cs+"_")) {
				return fmt.Errorf("SET to character set %s, in which a backslash byte can end another character", tok.text)
			}
		}
	}
	for _, a := range splitAssignments(t) {
		if err := c.checkAssignment(a); err != nil {
			return err
		}
	}
	return nil
}

// splitAssignments splits a SET statement's tokens at top-level commas.
func splitAssignments(t []sqlToken) [][]sqlToken {
	var out [][]sqlToken
	depth, start := 0, 0
	for i, tok := range t {
		if tok.kind != 'p' {
			continue
		}
		switch tok.text {
		case "(":
			depth++
		case ")":
			depth--
		case ",":
			if depth == 0 {
				out = append(out, t[start:i])
				start = i + 1
			}
		}
	}
	return append(out, t[start:])
}

func (c *stmtCheck) checkAssignment(a []sqlToken) error {
	eq := len(a)
	for i, tok := range a {
		if tok.kind == 'p' && tok.text == "=" {
			eq = i
			break
		}
	}
	lhs := a[:eq]
	if n := len(lhs); n > 0 && lhs[n-1].kind == 'p' && lhs[n-1].text == ":" {
		lhs = lhs[:n-1]
	}
	var name string
	for _, tok := range lhs {
		if tok.is("GLOBAL") || tok.is("SESSION") || tok.is("LOCAL") || tok.is("PERSIST") || tok.is("PERSIST_ONLY") {
			continue
		}
		name += strings.ToUpper(tok.text)
	}
	for _, scope := range []string{"@@GLOBAL.", "@@SESSION.", "@@LOCAL.", "@@PERSIST.", "@@PERSIST_ONLY.", "@@"} {
		if strings.HasPrefix(name, scope) {
			name = strings.TrimPrefix(name, scope)
			break
		}
	}
	if eq == len(a) {
		if strings.Contains(name, "SQL_MODE") {
			return errors.New("SET of sql_mode in a form this check does not read")
		}
		return nil
	}
	rhs := a[eq+1:]
	switch {
	case name == "SQL_MODE":
		return c.checkSQLMode(rhs)
	case strings.HasPrefix(name, "@"):
		if c.savedModes == nil {
			c.savedModes = map[string]bool{}
		}
		c.savedModes[name] = len(rhs) == 1 && (rhs[0].is("@@SQL_MODE") || rhs[0].is("@@SESSION.SQL_MODE") || rhs[0].is("@@LOCAL.SQL_MODE"))
	}
	return nil
}

// checkSQLMode allows sql_mode to be set to one quoted string without
// NO_BACKSLASH_ESCAPES, or to a user variable saved from @@sql_mode.
func (c *stmtCheck) checkSQLMode(rhs []sqlToken) error {
	if len(rhs) == 1 {
		v := rhs[0]
		// Dropping backslashes keeps an escaped letter from hiding the word.
		if (v.kind == '\'' || v.kind == '"') && !strings.Contains(strings.ToUpper(strings.ReplaceAll(v.text, `\`, "")), "NO_BACKSLASH_ESCAPES") {
			return nil
		}
		if v.kind == 'w' && strings.HasPrefix(v.text, "@") && c.savedModes[strings.ToUpper(v.text)] {
			return nil
		}
	}
	return fmt.Errorf("SET sql_mode = %s: only a quoted mode list without NO_BACKSLASH_ESCAPES, or a variable saved from @@sql_mode, is allowed", describe(rhs, 4))
}

// describe renders up to n tokens for an error message.
func describe(t []sqlToken, n int) string {
	parts := make([]string, 0, n+1)
	for i, tok := range t {
		if i == n {
			parts = append(parts, "...")
			break
		}
		text := tok.text
		if len(text) > 40 {
			text = text[:40] + "..."
		}
		if tok.kind != 'w' && tok.kind != 'p' {
			text = string(tok.kind) + text + string(tok.kind)
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, " ")
}

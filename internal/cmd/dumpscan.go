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
// The scanner reads the input the way the mysql and mariadb clients do: '
// and " quotes with backslash escapes, ` quotes without them, # and "-- "
// comments, lines starting with # or "-- ", /* */ comments, and /*!NNNNN */
// and /*M!NNNNNN */ comments, whose contents the client and server treat as
// SQL. It assumes the session state importPreamble sets: no
// NO_BACKSLASH_ESCAPES or ANSI_QUOTES, and a character set in which a
// backslash byte is always a backslash. It refuses:
//
//   - a backslash outside quotes and comments, which the client reads as a
//     one-letter command (\! runs a shell command, \. runs a file, \r and \u
//     switch database, \T writes a file, \C changes how later bytes are read).
//     The one exception is \- , MariaDB's switch that turns client commands
//     off, which mariadb-dump writes on its first line;
//   - a backslash inside ` quotes, which dumps never contain;
//   - /*+ */ optimizer hints, inside which neither client reads quotes;
//   - inside /*! */ and /*M! */, a comment, a delimiter, or a quote that runs
//     past the closing */. A server older than the comment's version skips it
//     without reading quotes, so it must end where the client thinks it does;
//   - := outside quotes, which assigns a user variable;
//   - a line whose first word is one of clientLineCommands, or use naming a
//     database other than the target;
//   - a statement that checkHead does not allow. Its allowlist also excludes
//     every other long-form client command (quit, prompt, ...), which the
//     client recognizes at the start of a statement;
//   - a SET of sql_mode to anything but a list of modes that leave quoting
//     alone, a SET of the client character set to one in which a backslash
//     byte can end a character, and a SET of a user variable to anything but
//     a system variable. A system variable may be set back from a user
//     variable only if the user variable was saved from it and nothing else
//     in the dump, trigger and routine bodies included, mentions it;
//   - a table or other object name qualified with a database other than the
//     target;
//   - OUTFILE or DUMPFILE outside quotes, which write files on the server.
//
// DELIMITER at the start of a line between statements is allowed;
// mysqldump writes DELIMITER ;; around triggers and routines.
func scanDump(r io.Reader, database string) error {
	s := &dumpScanner{
		r:     bufio.NewReaderSize(r, 64<<10),
		delim: []byte(";"),
		line:  1,
		prev:  ' ',
		stmt:  stmtCheck{database: database, vars: &userVars{saved: map[string]string{}, poisoned: map[string]bool{}}},
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
	// cond is set inside /*! */ and /*M! */. dead is set inside
	// /*M!999999 */, which no server version runs.
	cond, dead bool
	// prev is the code byte before the current one, or a space after
	// whitespace, a comment or a newline.
	prev byte

	stmt stmtCheck
	// word is the code word being read, for the OUTFILE check.
	word     []byte
	wordLong bool
	// varName is the name of the user variable being read, after its @.
	varName        []byte
	inVar, varLong bool
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
			s.endVar()
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
	// The client drops a line starting with # or "-- " whole. mysql reads a
	// line starting with -- and then not a space as code, and mariadb drops
	// it at the start of a statement; read as code, the line cannot start a
	// statement this scanner allows.
	if head, _ := s.r.Peek(3); len(head) > 0 && (head[0] == '#' || bytes.HasPrefix(head, []byte("--")) && (len(head) == 2 || isSpace(head[2]))) {
		if s.cond {
			return true, false, errCommentInCond
		}
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
		if s.cond {
			return false, false, errors.New("DELIMITER inside a /*! comment")
		}
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
				s.prev = ' '
				s.endVar()
				s.stmt.space()
				return false, s.endWord()
			}
			return false, nil
		}
		switch s.state {
		case stComment:
			if c == '*' && s.next('/') {
				s.state, s.prev = stCode, ' '
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
	stops := []byte{s.quote, '\\', '\n'}
	if s.cond {
		stops = append(stops, '*')
	}
	n := bytes.IndexAny(buf, string(stops))
	if n < 0 {
		n = len(buf)
	}
	s.stmt.quoteText(buf[:n])
	_, _ = s.r.Discard(n)
}

func (s *dumpScanner) quoted(c byte) error {
	switch {
	case c == '\\' && s.quote == '`':
		return errors.New("backslash inside a ` quoted name: dumps never contain one, and the client and server do not read it as an escape")
	case c == '*' && s.cond && s.next('/'):
		return errors.New("*/ inside a quote within a /*! comment: a server that skips the comment ends it there")
	case c == '\\':
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
		if e == '*' && s.cond && s.next('/') {
			return errors.New("*/ inside a quote within a /*! comment: a server that skips the comment ends it there")
		}
		s.stmt.quoteText([]byte{'\\', e})
	case c == s.quote:
		s.state, s.prev = stCode, c
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
	prev := s.prev
	s.prev = c
	if !isWordByte(c) {
		if err := s.endWord(); err != nil {
			return err
		}
		if c != '.' {
			s.endVar()
		}
	}
	if c == s.delim[0] {
		if rest, _ := s.r.Peek(len(s.delim) - 1); bytes.Equal(rest, s.delim[1:]) {
			if s.cond {
				return errors.New("delimiter inside a /*! comment")
			}
			_, _ = s.r.Discard(len(s.delim) - 1)
			return s.stmt.end()
		}
	}
	if !s.dead {
		switch {
		case c == '@':
			s.startVar(prev)
		case c == ':':
			if b, _ := s.r.Peek(1); len(b) > 0 && b[0] == '=' {
				return errors.New(":= outside quotes assigns a user variable, which dumps do not do")
			}
		case s.inVar:
			if len(s.varName) < 64 {
				s.varName = append(s.varName, c)
			} else {
				s.varLong = true
			}
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
	case c == '#' || c == '-' && s.dashComment():
		if s.cond {
			return errCommentInCond
		}
		s.lineComment()
		return nil
	case c == '/' && s.next('*'):
		s.prev = ' '
		return s.openComment()
	case c == '*' && s.cond && s.next('/'):
		s.cond, s.dead, s.prev = false, false, ' '
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

// errCommentInCond refuses a comment inside /*! */, where a # or -- comment
// can hide the */ from the client but not from a server that skips the
// comment.
var errCommentInCond = errors.New("comment inside a /*! comment")

// startVar handles an @ read in code state after prev. It starts reading a
// user variable's name, and reports a user variable it cannot name as "".
func (s *dumpScanner) startVar(prev byte) {
	if prev == '@' || prev == '\'' || prev == '"' || prev == '`' {
		// The second @ of @@name, or the host of 'user'@'host'.
		return
	}
	b, _ := s.r.Peek(1)
	switch {
	case len(b) > 0 && b[0] == '@':
	case len(b) > 0 && isWordByte(b[0]):
		s.inVar, s.varLong, s.varName = true, false, s.varName[:0]
	default:
		s.stmt.userVar("")
	}
}

// endVar reports the user variable whose name was being read. A name with
// non-ASCII bytes is reported as "", since mysql may compare it ignoring
// accents.
func (s *dumpScanner) endVar() {
	if !s.inVar {
		return
	}
	s.inVar = false
	name := ""
	if !s.varLong && !bytes.ContainsFunc(s.varName, func(r rune) bool { return r >= 0x80 }) {
		name = "@" + strings.ToUpper(string(s.varName))
	}
	s.stmt.userVar(name)
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
	if s.cond {
		return errCommentInCond
	}
	b, _ := s.r.Peek(2)
	switch {
	case len(b) > 0 && b[0] == '!':
		_, _ = s.r.Discard(1)
	case len(b) > 1 && b[0] == 'M' && b[1] == '!':
		_, _ = s.r.Discard(2)
	case len(b) > 0 && b[0] == '+':
		return errors.New("optimizer hint /*+ */: dumps do not contain hints, and the client and server do not read quotes inside them")
	default:
		s.state = stComment
		return nil
	}
	digits, _ := s.r.Peek(6)
	n := 0
	for n < len(digits) && digits[n] >= '0' && digits[n] <= '9' {
		n++
	}
	_, _ = s.r.Discard(n)
	s.cond = true
	s.dead = string(digits[:n]) == "999999"
	// The client keeps the comment in the statement it is building.
	s.stmt.started = s.stmt.started || !s.dead
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
	// vars persists across statements. varRefs are the user variables the
	// statement mentions, and setVars those checkSet accounted for.
	vars    *userVars
	varRefs []string
	setVars map[string]bool
}

// userVars tracks which user variables still hold the value of the system
// variable they were saved from.
type userVars struct {
	// saved maps a user variable to the system variable last saved in it.
	saved map[string]string
	// poisoned are user variables a statement other than SET @v = @@name
	// mentions, which a trigger or routine might write later. all poisons
	// every variable, for a mention this scanner cannot name.
	poisoned map[string]bool
	all      bool
}

func (u *userVars) trusted(name, sysvar string) bool {
	return !u.all && !u.poisoned[name] && u.saved[name] == sysvar
}

// userVar records that the statement mentions a user variable; "" stands for
// one this scanner cannot name.
func (c *stmtCheck) userVar(name string) {
	c.varRefs = append(c.varRefs, name)
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
	for _, name := range c.varRefs {
		switch {
		case c.setVars[name]:
		case name == "":
			c.vars.all = true
		default:
			c.vars.poisoned[name] = true
		}
	}
	*c = stmtCheck{database: c.database, vars: c.vars, tokens: c.tokens[:0], cur: c.cur[:0], varRefs: c.varRefs[:0], err: c.err}
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
	case t[0].is("COMMIT"):
		return true, nil
	case t[0].is("INSERT"), t[0].is("REPLACE"):
		i := 1
		for i < len(t) && (t[i].is("LOW_PRIORITY") || t[i].is("DELAYED") || t[i].is("HIGH_PRIORITY") || t[i].is("IGNORE") || t[i].is("INTO")) {
			i++
		}
		return c.checkObjectName(i, final)
	case t[0].is("LOCK"), t[0].is("UNLOCK"):
		if len(t) < 2 {
			return more()
		}
		switch {
		case !t[1].is("TABLES") && !t[1].is("TABLE"):
		case t[0].is("UNLOCK"):
			return true, nil
		case !final:
			return false, nil
		default:
			return true, c.checkAllNames(t[2:])
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
		name := i + 2 // index of the token after kind
		switch {
		case ddlObjects[kind] && verb == "DROP":
			if !final {
				return false, nil
			}
			return true, c.checkAllNames(c.tokens[name:])
		case kind == "TABLE" && verb == "ALTER":
			return c.checkAlterTable(name, final)
		case ddlObjects[kind] && verb == "CREATE":
			ok, err := c.checkObjectName(name, final)
			if !ok || err != nil || kind != "TRIGGER" {
				return ok, err
			}
			return c.checkTriggerTable(name, final)
		case (kind == "DATABASE" || kind == "SCHEMA") && verb != "DROP":
			return c.checkDatabaseName(verb, c.tokens[name:], final)
		}
		return false, fmt.Errorf("%s %s is not a statement mysqldump writes", verb, kind)
	}
	if final {
		return false, fmt.Errorf("statement %s is not one mysqldump writes", describe(c.tokens, 4))
	}
	return false, nil
}

// checkObjectName checks the name at c.tokens[i], after any IF [NOT] EXISTS.
// It needs the token after the name to see a qualifier written apart.
func (c *stmtCheck) checkObjectName(i int, final bool) (bool, error) {
	t := c.tokens
	for i < len(t) && (t[i].is("IF") || t[i].is("NOT") || t[i].is("EXISTS")) {
		i++
	}
	if i+1 >= len(t) && !final {
		return false, nil
	}
	if i < len(t) {
		if err := c.checkQualifier(t[i:]); err != nil {
			return false, err
		}
	}
	return true, nil
}

// checkTriggerTable checks the table named after ON in CREATE TRIGGER name
// {BEFORE|AFTER} event ON table.
func (c *stmtCheck) checkTriggerTable(i int, final bool) (bool, error) {
	for j := i + 1; j < len(c.tokens) && j < i+8; j++ {
		if c.tokens[j].is("ON") {
			return c.checkObjectName(j+1, final)
		}
	}
	if len(c.tokens) < i+8 && !final {
		return false, nil
	}
	return false, fmt.Errorf("CREATE TRIGGER %s is not one mysqldump writes", describe(c.tokens[i:], 6))
}

// checkAlterTable allows only ALTER TABLE name {DISABLE|ENABLE} KEYS, the
// one ALTER TABLE mysqldump writes.
func (c *stmtCheck) checkAlterTable(i int, final bool) (bool, error) {
	if !final {
		return false, nil
	}
	t := c.tokens
	if i < len(t) {
		if err := c.checkQualifier(t[i:]); err != nil {
			return false, err
		}
		if e := nameEnd(t, i); len(t) == e+2 && (t[e].is("DISABLE") || t[e].is("ENABLE")) && t[e+1].is("KEYS") {
			return true, nil
		}
	}
	return false, fmt.Errorf("ALTER TABLE %s is not one mysqldump writes", describe(t[min(i, len(t)):], 5))
}

// checkAllNames checks every name in a DROP or LOCK TABLES statement.
func (c *stmtCheck) checkAllNames(t []sqlToken) error {
	for i, tok := range t {
		if tok.kind == 'w' && strings.HasPrefix(tok.text, ".") {
			continue // the rest of a qualified name checked at its start
		}
		if tok.kind == 'w' || tok.kind == '`' {
			if err := c.checkQualifier(t[i:]); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkQualifier refuses a name starting at t[0] that is qualified with a
// database other than the target: db.name, `db`.`name` or `db` . `name`.
func (c *stmtCheck) checkQualifier(t []sqlToken) error {
	var schema string
	switch {
	case t[0].kind == 'w' && strings.Contains(t[0].text, "."):
		schema = t[0].text[:strings.IndexByte(t[0].text, '.')]
	case len(t) > 1 && t[1].kind == 'w' && strings.HasPrefix(t[1].text, "."):
		schema = t[0].text
	default:
		return nil
	}
	if c.database != "" && schema == c.database {
		return nil
	}
	return fmt.Errorf("name qualified with database %q: the import may only write to the site database %q", schema, c.database)
}

// nameEnd returns the index of the token after the name starting at t[i].
func nameEnd(t []sqlToken, i int) int {
	j := i + 1
	switch {
	case t[i].kind == 'w' && strings.HasSuffix(t[i].text, "."):
		return j + 1 // db.`name`
	case j < len(t) && t[j].kind == 'w' && t[j].text == ".":
		return j + 2 // `db` . `name`
	case j < len(t) && t[j].kind == 'w' && strings.HasPrefix(t[j].text, "."):
		return j + 1 // `db`.name
	}
	return j
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
// where this scanner does not, and tracks user variables saved from system
// variables.
func (c *stmtCheck) checkSet(t []sqlToken) error {
	if len(t) > 0 && t[0].is("STATEMENT") {
		return errors.New("SET STATEMENT is not one mysqldump writes")
	}
	for _, tok := range t {
		if tok.kind != 'p' && unsafeCharset(tok.text) {
			return fmt.Errorf("SET to character set %s, in which a backslash byte can end another character", tok.text)
		}
	}
	c.setVars = map[string]bool{}
	for _, a := range splitAssignments(t) {
		if err := c.checkAssignment(a); err != nil {
			return err
		}
	}
	return nil
}

func unsafeCharset(name string) bool {
	up := strings.ToUpper(name)
	for _, cs := range unsafeCharsets {
		if up == cs || strings.HasPrefix(up, cs+"_") {
			return true
		}
	}
	return false
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

// checkAssignment allows SET @v = @@name, which saves a system variable, and
// SET name = @v only when @v holds name's saved value. sql_mode and the client
// character set may otherwise be set only to values checkSQLMode and
// checkClientCharset allow.
func (c *stmtCheck) checkAssignment(a []sqlToken) error {
	if len(a) == 0 {
		return nil
	}
	if a[0].is("NAMES") || a[0].is("CHARSET") || a[0].is("CHARACTER") {
		// SET NAMES cs [COLLATE x], SET CHARSET cs, SET CHARACTER SET cs.
		i := 1
		if a[0].is("CHARACTER") {
			i = 2
		}
		if i >= len(a) {
			return fmt.Errorf("SET %s without a character set", describe(a, 3))
		}
		return checkClientCharset(a[i])
	}
	eq := len(a)
	for i, tok := range a {
		if tok.kind == 'p' && tok.text == "=" {
			eq = i
			break
		}
	}
	var name string
	for _, tok := range a[:eq] {
		if tok.is("GLOBAL") || tok.is("SESSION") || tok.is("LOCAL") || tok.is("PERSIST") || tok.is("PERSIST_ONLY") {
			continue
		}
		name += strings.ToUpper(tok.text)
	}
	name = trimScope(name)
	lexical := name == "SQL_MODE" || name == "CHARACTER_SET_CLIENT"
	if eq == len(a) {
		if lexical || strings.HasPrefix(name, "@") {
			return fmt.Errorf("SET %s in a form this check does not read", describe(a, 4))
		}
		return nil
	}
	rhs := a[eq+1:]
	if isUserVar(name) {
		sysvar, ok := savedFrom(rhs)
		if !ok {
			return fmt.Errorf("SET %s = %s: a user variable may only be saved from a session system variable", a[0].text, describe(rhs, 4))
		}
		c.vars.saved[name] = sysvar
		c.setVars[name] = true
		return nil
	}
	if len(rhs) == 1 && rhs[0].kind == 'w' && isUserVar(rhs[0].text) {
		v := strings.ToUpper(rhs[0].text)
		c.setVars[v] = true
		if !c.vars.trusted(v, name) {
			return fmt.Errorf("SET %s = %s: the variable was not saved from @@%s, or the dump mentions it elsewhere", strings.ToLower(name), rhs[0].text, strings.ToLower(name))
		}
		return nil
	}
	switch name {
	case "SQL_MODE":
		return checkSQLMode(rhs)
	case "CHARACTER_SET_CLIENT":
		if len(rhs) != 1 {
			return fmt.Errorf("SET character_set_client = %s: only a named character set is allowed", describe(rhs, 4))
		}
		return checkClientCharset(rhs[0])
	}
	return nil
}

// trimScope removes a leading @@, @@SESSION. or @@LOCAL. from an upper-case
// system variable name. A @@GLOBAL. or @@PERSIST. prefix stays.
func trimScope(name string) string {
	for _, scope := range []string{"@@SESSION.", "@@LOCAL.", "@@"} {
		if rest, ok := strings.CutPrefix(name, scope); ok {
			return rest
		}
	}
	return name
}

func isUserVar(word string) bool {
	return strings.HasPrefix(word, "@") && !strings.HasPrefix(word, "@@")
}

// savedFrom returns the session system variable rhs reads, if rhs is exactly
// @@name, @@SESSION.name or @@LOCAL.name.
func savedFrom(rhs []sqlToken) (string, bool) {
	if len(rhs) != 1 || rhs[0].kind != 'w' || !strings.HasPrefix(rhs[0].text, "@@") {
		return "", false
	}
	name := trimScope(strings.ToUpper(rhs[0].text))
	if name == "" || strings.Contains(name, ".") || strings.Contains(name, "@") {
		return "", false
	}
	return name, true
}

// safeSQLModes are the sql_mode values that leave how quotes and backslashes
// are read alone. NO_BACKSLASH_ESCAPES and ANSI_QUOTES change it, as do the
// combination modes that include ANSI_QUOTES (ANSI, DB2, MAXDB, MSSQL,
// ORACLE, POSTGRESQL), so none of those are here.
var safeSQLModes = map[string]bool{
	"": true, "ALLOW_INVALID_DATES": true, "EMPTY_STRING_IS_NULL": true, "ERROR_FOR_DIVISION_BY_ZERO": true,
	"HIGH_NOT_PRECEDENCE": true, "IGNORE_BAD_TABLE_OPTIONS": true, "IGNORE_SPACE": true, "NO_AUTO_CREATE_USER": true,
	"NO_AUTO_VALUE_ON_ZERO": true, "NO_DIR_IN_CREATE": true, "NO_ENGINE_SUBSTITUTION": true, "NO_FIELD_OPTIONS": true,
	"NO_KEY_OPTIONS": true, "NO_TABLE_OPTIONS": true, "NO_UNSIGNED_SUBTRACTION": true, "NO_ZERO_DATE": true,
	"NO_ZERO_IN_DATE": true, "ONLY_FULL_GROUP_BY": true, "PAD_CHAR_TO_FULL_LENGTH": true, "PIPES_AS_CONCAT": true,
	"REAL_AS_FLOAT": true, "SIMULTANEOUS_ASSIGNMENT": true, "STRICT_ALL_TABLES": true, "STRICT_TRANS_TABLES": true,
	"TIME_ROUND_FRACTIONAL": true, "TIME_TRUNCATE_FRACTIONAL": true, "TRADITIONAL": true,
}

// checkSQLMode allows sql_mode to be set to one quoted list of safeSQLModes.
func checkSQLMode(rhs []sqlToken) error {
	if len(rhs) != 1 || rhs[0].kind != '\'' && rhs[0].kind != '"' {
		return fmt.Errorf("SET sql_mode = %s: only a quoted list of modes, or a variable saved from @@sql_mode, is allowed", describe(rhs, 4))
	}
	if strings.Contains(rhs[0].text, `\`) {
		return fmt.Errorf("SET sql_mode = %s: a backslash in the mode list is not allowed", describe(rhs, 1))
	}
	for _, mode := range strings.Split(rhs[0].text, ",") {
		if mode = strings.ToUpper(strings.TrimSpace(mode)); !safeSQLModes[mode] {
			return fmt.Errorf("SET sql_mode: mode %s is not allowed; the import allows only modes that leave how quotes and backslashes are read alone", mode)
		}
	}
	return nil
}

// checkClientCharset allows a named character set in which a backslash byte
// is always a backslash. DEFAULT and numeric ids, which name the server's
// choice, are refused.
func checkClientCharset(t sqlToken) error {
	name := t.text
	valid := (t.kind == 'w' || t.kind == '\'' || t.kind == '"') && name != "" && !strings.EqualFold(name, "DEFAULT")
	for i := 0; valid && i < len(name); i++ {
		ch := name[i]
		valid = ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || i > 0 && (ch >= '0' && ch <= '9' || ch == '_')
	}
	if !valid {
		return fmt.Errorf("character_set_client %s: only a named character set is allowed", describe([]sqlToken{t}, 1))
	}
	if unsafeCharset(name) {
		return fmt.Errorf("SET to character set %s, in which a backslash byte can end another character", name)
	}
	return nil
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

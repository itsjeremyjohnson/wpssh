package cmd

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// mysqldumpSample is shaped like `wp db export -` output from mysqldump 8.0
// on a server with GTIDs, with a trigger and a routine, and holds string data that mentions client
// commands. Its database is wp_acme.
const mysqldumpSample = `-- MySQL dump 10.13  Distrib 8.0.39, for Linux (x86_64)
--
-- Host: localhost    Database: wp_acme
-- ------------------------------------------------------
-- Server version	8.0.39

/*!40101 SET @OLD_CHARACTER_SET_CLIENT=@@CHARACTER_SET_CLIENT */;
/*!40101 SET @OLD_CHARACTER_SET_RESULTS=@@CHARACTER_SET_RESULTS */;
/*!40101 SET @OLD_COLLATION_CONNECTION=@@COLLATION_CONNECTION */;
/*!50503 SET NAMES utf8mb4 */;
/*!40103 SET @OLD_TIME_ZONE=@@TIME_ZONE */;
/*!40103 SET TIME_ZONE='+00:00' */;
/*!40014 SET @OLD_UNIQUE_CHECKS=@@UNIQUE_CHECKS, UNIQUE_CHECKS=0 */;
/*!40014 SET @OLD_FOREIGN_KEY_CHECKS=@@FOREIGN_KEY_CHECKS, FOREIGN_KEY_CHECKS=0 */;
/*!40101 SET @OLD_SQL_MODE=@@SQL_MODE, SQL_MODE='NO_AUTO_VALUE_ON_ZERO' */;
/*!40111 SET @OLD_SQL_NOTES=@@SQL_NOTES, SQL_NOTES=0 */;
SET @MYSQLDUMP_TEMP_LOG_BIN = @@SESSION.SQL_LOG_BIN;
SET @@SESSION.SQL_LOG_BIN= 0;

--
-- GTID state at the beginning of the backup 
--

SET @@GLOBAL.GTID_PURGED=/*!80000 '+'*/ '3e11fa47-71ca-11e1-9e33-c80aa9429562:1-5';

--
-- Table structure for table ` + "`wp_posts`" + `
--

DROP TABLE IF EXISTS ` + "`wp_posts`" + `;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE ` + "`wp_posts`" + ` (
  ` + "`ID`" + ` bigint unsigned NOT NULL AUTO_INCREMENT,
  ` + "`post_content`" + ` longtext NOT NULL,
  ` + "`source`" + ` varchar(20) NOT NULL DEFAULT 'system',
  PRIMARY KEY (` + "`ID`" + `)
) ENGINE=InnoDB AUTO_INCREMENT=3 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Dumping data for table ` + "`wp_posts`" + `
--

LOCK TABLES ` + "`wp_posts`" + ` WRITE;
/*!40000 ALTER TABLE ` + "`wp_posts`" + ` DISABLE KEYS */;
INSERT INTO ` + "`wp_posts`" + ` VALUES (1,'Run \\! id or system rm -rf / -- or source /etc/passwd;\nuse other_db;\n\\. x.sql','system'),(2,'it\'s \"quoted\" ` + "`tick`" + ` /* not a comment */ # nor this; DELIMITER $$ \\\\! still data','source');
/*!40000 ALTER TABLE ` + "`wp_posts`" + ` ENABLE KEYS */;
UNLOCK TABLES;
/*!50003 SET @saved_cs_client      = @@character_set_client */ ;
/*!50003 SET @saved_cs_results     = @@character_set_results */ ;
/*!50003 SET @saved_col_connection = @@collation_connection */ ;
/*!50003 SET character_set_client  = utf8mb4 */ ;
/*!50003 SET character_set_results = utf8mb4 */ ;
/*!50003 SET collation_connection  = utf8mb4_0900_ai_ci */ ;
/*!50003 SET @saved_sql_mode       = @@sql_mode */ ;
/*!50003 SET sql_mode              = 'ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION' */ ;
DELIMITER ;;
/*!50003 CREATE*/ /*!50017 DEFINER=` + "`acme`@`localhost`" + `*/ /*!50003 TRIGGER ` + "`wp_posts_bi`" + ` BEFORE INSERT ON ` + "`wp_posts`" + ` FOR EACH ROW BEGIN
  IF NEW.source = '' THEN
    SET NEW.source = 'system';
  END IF;
END */;;
DELIMITER ;
/*!50003 SET sql_mode              = @saved_sql_mode */ ;
/*!50003 SET character_set_client  = @saved_cs_client */ ;
/*!50003 SET character_set_results = @saved_cs_results */ ;
/*!50003 SET collation_connection  = @saved_col_connection */ ;
/*!50003 DROP PROCEDURE IF EXISTS ` + "`count_posts`" + ` */;
DELIMITER ;;
CREATE DEFINER=` + "`acme`@`localhost`" + ` PROCEDURE ` + "`count_posts`" + `()
BEGIN
  SELECT COUNT(*) FROM wp_posts;
END ;;
DELIMITER ;
SET @@SESSION.SQL_LOG_BIN = @MYSQLDUMP_TEMP_LOG_BIN;
/*!40103 SET TIME_ZONE=@OLD_TIME_ZONE */;

/*!40101 SET SQL_MODE=@OLD_SQL_MODE */;
/*!40014 SET FOREIGN_KEY_CHECKS=@OLD_FOREIGN_KEY_CHECKS */;
/*!40014 SET UNIQUE_CHECKS=@OLD_UNIQUE_CHECKS */;
/*!40101 SET CHARACTER_SET_CLIENT=@OLD_CHARACTER_SET_CLIENT */;
/*!40101 SET CHARACTER_SET_RESULTS=@OLD_CHARACTER_SET_RESULTS */;
/*!40101 SET COLLATION_CONNECTION=@OLD_COLLATION_CONNECTION */;
/*!40111 SET SQL_NOTES=@OLD_SQL_NOTES */;

-- Dump completed on 2026-10-06 12:00:00
`

// mariadbSample is shaped like mariadb-dump 11 output: the sandbox line, a
// view, autocommit handling and a USE of the site database.
const mariadbSample = "/*M!999999\\- enable the sandbox mode */ \n" +
	"-- MariaDB dump 10.19-11.4.3-MariaDB, for debian-linux-gnu (x86_64)\n" +
	"/*!40101 SET NAMES utf8mb4 */;\n" +
	"/*M!100616 SET @OLD_NOTE_VERBOSITY=@@NOTE_VERBOSITY, NOTE_VERBOSITY=0 */;\n" +
	"CREATE DATABASE /*!32312 IF NOT EXISTS*/ `wp_acme` /*!40100 DEFAULT CHARACTER SET utf8mb4 */;\n" +
	"USE `wp_acme`;\n" +
	"/*!50001 DROP VIEW IF EXISTS `recent`*/;\n" +
	"/*!50001 CREATE ALGORITHM=UNDEFINED */\n" +
	"/*!50013 DEFINER=`acme`@`%` SQL SECURITY DEFINER */\n" +
	"/*!50001 VIEW `recent` AS select `wp_posts`.`ID` AS `ID` from `wp_posts` */;\n" +
	"LOCK TABLES `wp_options` WRITE;\n" +
	"set autocommit=0;\n" +
	"REPLACE INTO `wp_options` VALUES (1,'siteurl','https://acme.example','yes');\n" +
	"commit;\n" +
	"UNLOCK TABLES;\n" +
	"/*!50003 ALTER DATABASE `wp_acme` CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci */ ;\n" +
	"/*M!100616 SET NOTE_VERBOSITY=@OLD_NOTE_VERBOSITY */;\n" +
	"-- Dump completed on 2026-10-06 12:00:00\r\n"

func TestScanDumpAcceptsDumps(t *testing.T) {
	longValue := strings.Repeat(`x\'y; system \\! `, 1<<16) // ~1 MiB in one value
	cases := map[string]string{
		"mysqldump":               mysqldumpSample,
		"mysqldump crlf":          strings.ReplaceAll(mysqldumpSample, "\n", "\r\n"),
		"mariadb-dump":            mariadbSample,
		"very long line":          "INSERT INTO `wp_postmeta` VALUES (1,'" + longValue + "');\n-- Dump completed\n",
		"no final newline":        "INSERT INTO t VALUES (1)",
		"comment-only line":       "  -- system id\n# source x.sql\n/* \\! id */\nINSERT INTO t VALUES (1);\n",
		"site database qualified": "INSERT INTO wp_acme.wp_posts VALUES (1);\nDROP TABLE IF EXISTS `wp_acme`.`wp_old`;\n",
		"import preamble":         importPreamble,
	}
	for name, dump := range cases {
		t.Run(name, func(t *testing.T) {
			if err := scanDump(strings.NewReader(dump), "wp_acme"); err != nil {
				t.Fatal(err)
			}
			// Reads of one byte at a time split every token across reads.
			if err := scanDump(&oneByteReader{r: strings.NewReader(dump)}, "wp_acme"); err != nil {
				t.Fatalf("one byte per read: %v", err)
			}
		})
	}
}

func TestScanDumpRefusesClientCommands(t *testing.T) {
	const ok = "INSERT INTO t VALUES (1);\n"
	cases := []struct {
		name, dump, want string
	}{
		{"backslash system", `\! id`, `"\\!"`},
		{"backslash system after whitespace", " \t\\! id", `"\\!"`},
		{"backslash system mid line", "INSERT INTO t VALUES (1); \\! id\n", `"\\!"`},
		{"backslash system after string", "INSERT INTO t VALUES ('a\\\\'); \\! id\n", `"\\!"`},
		{"backslash system in conditional", "/*!40101 \\! id */;\n", `"\\!"`},
		{"backslash source", "\\. /tmp/x.sql\n", `"\\."`},
		{"backslash connect", "\\r other_db\n", `"\\r"`},
		{"backslash use", "\\u other_db\n", `"\\u"`},
		{"backslash tee", "\\T /tmp/out\n", `"\\T"`},
		{"backslash charset", "\\C gbk\n", `"\\C"`},
		{"backslash delimiter", "\\d $$\n", `"\\d"`},
		{"system", "system id\n", `"system" runs a shell command`},
		{"system uppercase crlf", "SYSTEM id\r\n", `"system"`},
		{"system indented", "   system id", `"system"`},
		{"system mid statement", "INSERT INTO t VALUES\n system id\n(1);\n", `"system"`},
		{"system after delimiter line", "DELIMITER ;;\nsystem id\n", `"system"`},
		{"source", "source /tmp/x.sql\n", `"source" runs a file`},
		{"source with delimiter", "source /tmp/x.sql;\n", `"source"`},
		{"connect", "connect other_db\n", `"connect"`},
		{"tee", "tee /tmp/out\n", `"tee"`},
		{"pager", "pager sh -c id\n", `"pager"`},
		{"use other database", "USE other_db;\n", `USE "other_db"`},
		{"use other database quoted", "use `wp_acme2`\n", `USE "wp_acme2"`},
		{"use other database mid line", "INSERT INTO t VALUES (1); USE other_db;\n", `USE "other_db"`},
		{"use in conditional", "/*!40101 USE other_db */;\n", `USE "other_db"`},
		{"quit at statement start", "quit\n", "statement quit is not one mysqldump writes"},
		{"select", "SELECT 1;\n", "SELECT"},
		{"prepare", "PREPARE s FROM @x;\n", "PREPARE"},
		{"grant", "GRANT ALL ON *.* TO x;\n", "GRANT"},
		{"drop database", "DROP DATABASE wp_acme;\n", "DROP DATABASE"},
		{"create database other", "CREATE DATABASE `other_db`;\n", `CREATE DATABASE "other_db"`},
		{"create user", "CREATE USER x IDENTIFIED BY 'y';\n", "CREATE USER"},
		{"outfile in insert", "INSERT INTO t SELECT * FROM u INTO OUTFILE '/tmp/x';\n", "OUTFILE"},
		{"dumpfile in trigger body", "DELIMITER ;;\nCREATE TRIGGER t BEFORE INSERT ON x FOR EACH ROW BEGIN\nSELECT 1 INTO DUMPFILE '/tmp/x';\nEND;;\n", "DUMPFILE"},
		{"no backslash escapes", "SET sql_mode = 'NO_BACKSLASH_ESCAPES';\n", "NO_BACKSLASH_ESCAPES"},
		{"no backslash escapes escaped", "/*!40101 SET SQL_MODE='NO\\_BACKSLASH_ESCAPES' */;\n", "backslash in the mode list"},
		{"sql mode concat", "SET @@session.sql_mode = CONCAT('NO_BACKSLASH', '_ESCAPES');\n", "sql_mode"},
		{"sql mode adjacent literals", "SET sql_mode = 'NO_BACKSLASH' '_ESCAPES';\n", "sql_mode"},
		{"sql mode number", "SET SESSION sql_mode = 2097152;\n", "sql_mode"},
		{"sql mode from tampered variable", "SET @OLD_SQL_MODE = 'NO_BACKSLASH_ESCAPES';\nSET SQL_MODE=@OLD_SQL_MODE;\n", "user variable"},
		{"gbk names", "/*!40101 SET NAMES gbk */;\n", "character set gbk"},
		{"sjis client charset", "SET character_set_client = 'sjis';\n", "character set sjis"},
		{"delimiter with letters", "DELIMITER END\n", "unsupported DELIMITER"},
		{"delimiter with two arguments", "DELIMITER ;; x\n", "unsupported DELIMITER"},
		{"delimiter inside statement", "INSERT INTO t VALUES\nDELIMITER ;;\n", "DELIMITER inside a statement"},
		{"comment line hides comment start", "--x /*\n\\! id\n*/\n", "statement -"},
		{"hint is code", "INSERT /*+ \\! id */ INTO t VALUES (1);\n", "optimizer hint"},
		{"later in a long line", "INSERT INTO t VALUES ('" + strings.Repeat("a", 1<<20) + "'); \\! id\n", `"\\!"`},
		// The client and server end a backtick identifier at the next
		// backtick; a backslash does not escape it.
		{"backslash before backtick", "CREATE TABLE `t\\` (x INT);\nSELECT 1 INTO OUTFILE \"/tmp/review-outfile\";\n-- `\n", "backslash inside"},
		// Neither the client nor the server reads quotes inside /*+ */.
		{"quote in optimizer hint", "COMMIT /*+ ' */;\nSELECT 1 INTO OUTFILE \"/tmp/review-outfile\";\n-- '\n", "optimizer hint"},
		{"user variable assigned in insert", "CREATE TEMPORARY TABLE t (x TEXT);\nSET @old=@@sql_mode;\nINSERT INTO t VALUES (@old:='NO_BACKSLASH_ESCAPES');\nSET sql_mode=@old;\n", ":="},
		{"saved mode rewritten by trigger", "/*!50003 SET @saved_sql_mode = @@sql_mode */ ;\nDELIMITER ;;\nCREATE TRIGGER x BEFORE INSERT ON wp_posts FOR EACH ROW SET @saved_sql_mode = 'NO_BACKSLASH_ESCAPES';;\nDELIMITER ;\nSET @saved_sql_mode = @@sql_mode;\nINSERT INTO wp_posts VALUES (1);\nSET sql_mode = @saved_sql_mode;\n", "sql_mode"},
		{"saved mode rewritten by trigger select into", "SET @m = @@sql_mode;\nDELIMITER ;;\nCREATE TRIGGER x BEFORE INSERT ON wp_posts FOR EACH ROW SELECT 'NO_BACKSLASH_ESCAPES' INTO@m;;\nDELIMITER ;\nINSERT INTO wp_posts VALUES (1);\nSET sql_mode = @m;\n", "sql_mode"},
		// mysql may compare user variable names without accents.
		{"saved mode rewritten under accented name", "SET @saved_sql_mode = @@sql_mode;\nDELIMITER ;;\nCREATE TRIGGER x BEFORE INSERT ON wp_posts FOR EACH ROW SET @s\u00e1ved_sql_mode = 'NO_BACKSLASH_ESCAPES';;\nDELIMITER ;\nINSERT INTO wp_posts VALUES (1);\nSET sql_mode = @saved_sql_mode;\n", "sql_mode"},
		{"ansi quotes", "/*!40101 SET SQL_MODE='ANSI_QUOTES' */;\n", "ANSI_QUOTES"},
		{"ansi combination mode", "SET sql_mode = 'ANSI';\n", "ANSI"},
		{"client charset from another variable", "SET @v = @@time_zone;\nSET character_set_client = @v;\n", "character_set_client"},
		{"client charset default", "SET NAMES DEFAULT;\n", "DEFAULT"},
		{"client charset by number", "SET character_set_client = 28;\n", "character_set_client"},
		{"qualified drop", "DROP TABLE other_db.wp_posts;\n", `"other_db"`},
		{"qualified insert", "INSERT INTO `other_db`.`wp_posts` VALUES (1);\n", `"other_db"`},
		{"qualified create", "CREATE TABLE IF NOT EXISTS other_db.t (x INT);\n", `"other_db"`},
		{"qualified trigger table", "CREATE TRIGGER x BEFORE INSERT ON `other_db` . `t` FOR EACH ROW SET NEW.a = 1;\n", `"other_db"`},
		{"qualified lock", "LOCK TABLES wp_posts WRITE, `other_db`.t WRITE;\n", `"other_db"`},
		{"alter table rename", "ALTER TABLE wp_posts RENAME TO other_db.wp_posts;\n", "ALTER TABLE"},
		// A server older than the version skips the comment without reading
		// quotes, so it ends at */ while the client is still in the quote.
		{"comment end inside quote in versioned comment", "INSERT INTO t VALUES (1) /*!99999 ' */, (2); SELECT 1 INTO OUTFILE \"/tmp/x\"; -- ' */;\n", "inside a quote"},
		{"escaped comment end inside quote in versioned comment", "INSERT INTO t VALUES (1) /*!99999 '\\*/, (2); SELECT 1 INTO OUTFILE \"/tmp/x\"; -- ' */;\n", "inside a quote"},
		// The client keeps /*! */ in the statement, so it reads the DELIMITER
		// line as SQL.
		{"delimiter after versioned comment", "/*!40101 */\nDELIMITER ;;\nINSERT INTO t VALUES (1);\n", "DELIMITER inside a statement"},
		{"line comment in versioned comment", "/*!99999 -- */ SELECT 1 INTO OUTFILE '/tmp/x';\n", "comment inside"},
		{"comment line in versioned comment", "/*!40101 SET x = 1\n-- */ SELECT 1 INTO OUTFILE '/tmp/x';\n", "comment inside"},
		// mysql reads a line starting with --x as code, so ' opens a quote.
		{"dashes without space at statement start", "--x '\nINSERT INTO t VALUES (' \\! id\n');\n", "statement -"},
		// The client reads --x mid-statement as code, so /* opens a comment.
		{"dashes without space mid statement", "INSERT INTO t VALUES (1\n--1 /*\n' */ ); SELECT 1 INTO OUTFILE '/tmp/x'; -- '\n", "SELECT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, r := range []io.Reader{strings.NewReader(ok + tc.dump), &oneByteReader{r: strings.NewReader(ok + tc.dump)}} {
				err := scanDump(r, "wp_acme")
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("err = %v, want it to contain %q", err, tc.want)
				}
			}
		})
	}
}

func TestScanDumpReportsLine(t *testing.T) {
	err := scanDump(strings.NewReader("-- header\nINSERT INTO t VALUES ('a\nb');\n\n\\! id\n"), "wp_acme")
	if err == nil || !strings.Contains(err.Error(), "line 5:") {
		t.Fatalf("err = %v, want line 5", err)
	}
}

// oneByteReader returns one byte per Read.
type oneByteReader struct{ r io.Reader }

func (o *oneByteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return o.r.Read(p[:1])
}

func BenchmarkScanDump(b *testing.B) {
	row := "(1,'" + strings.Repeat(`lorem ipsum \'dolor\' sit amet, `, 30) + "',NULL,42),"
	dump := []byte("INSERT INTO `wp_posts` VALUES " + strings.Repeat(row, 10000) + "(2,'x',NULL,1);\n")
	b.SetBytes(int64(len(dump)))
	for b.Loop() {
		if err := scanDump(bytes.NewReader(dump), "wp_acme"); err != nil {
			b.Fatal(err)
		}
	}
}

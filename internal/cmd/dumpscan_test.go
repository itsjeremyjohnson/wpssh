package cmd

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// mysqldumpSample is shaped like `wp db export -` output from mysqldump 8.0
// with a trigger and a routine, and holds string data that mentions client
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
		"mysqldump":         mysqldumpSample,
		"mysqldump crlf":    strings.ReplaceAll(mysqldumpSample, "\n", "\r\n"),
		"mariadb-dump":      mariadbSample,
		"very long line":    "INSERT INTO `wp_postmeta` VALUES (1,'" + longValue + "');\n-- Dump completed\n",
		"no final newline":  "INSERT INTO t VALUES (1)",
		"comment-only line": "  -- system id\n# source x.sql\n/* \\! id */\nINSERT INTO t VALUES (1);\n",
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
		{"no backslash escapes escaped", "/*!40101 SET SQL_MODE='NO\\_BACKSLASH_ESCAPES' */;\n", "NO_BACKSLASH_ESCAPES"},
		{"sql mode concat", "SET @@session.sql_mode = CONCAT('NO_BACKSLASH', '_ESCAPES');\n", "sql_mode"},
		{"sql mode adjacent literals", "SET sql_mode = 'NO_BACKSLASH' '_ESCAPES';\n", "sql_mode"},
		{"sql mode number", "SET SESSION sql_mode = 2097152;\n", "sql_mode"},
		{"sql mode from tampered variable", "SET @OLD_SQL_MODE = 'NO_BACKSLASH_ESCAPES';\nSET SQL_MODE=@OLD_SQL_MODE;\n", "sql_mode"},
		{"gbk names", "/*!40101 SET NAMES gbk */;\n", "character set gbk"},
		{"sjis client charset", "SET character_set_client = 'sjis';\n", "character set sjis"},
		{"delimiter with letters", "DELIMITER END\n", "unsupported DELIMITER"},
		{"delimiter with two arguments", "DELIMITER ;; x\n", "unsupported DELIMITER"},
		{"delimiter inside statement", "INSERT INTO t VALUES\nDELIMITER ;;\n", "DELIMITER inside a statement"},
		{"comment line hides comment start", "--x /*\n\\! id\n*/\n", `"\\!"`},
		{"hint is code", "INSERT /*+ \\! id */ INTO t VALUES (1);\n", `"\\!"`},
		{"later in a long line", "INSERT INTO t VALUES ('" + strings.Repeat("a", 1<<20) + "'); \\! id\n", `"\\!"`},
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

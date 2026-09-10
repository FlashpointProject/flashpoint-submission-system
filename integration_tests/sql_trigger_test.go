package integration_tests

import (
	"fmt"
	"regexp"
	"strings"
)

var testTriggerHeader = regexp.MustCompile(`(?s)^CREATE TRIGGER (\w+) (BEFORE|AFTER) (INSERT|UPDATE|DELETE) ON (\w+) FOR EACH ROW\s+BEGIN\s*(.*)\s+END$`)
var testTriggerSignal = regexp.MustCompile(`SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT=('([^']|'')*')`)

// The fault fixtures share their conditions and prerequisite assertions across
// engines. Only trigger declaration and exception syntax differ.
func testTriggerSQL(query string) string {
	if !postgresSubmissionTests() {
		return query
	}
	m := testTriggerHeader.FindStringSubmatch(query)
	if m == nil {
		panic("unsupported test trigger: " + query)
	}
	body := testTriggerSignal.ReplaceAllString(m[5], `RAISE EXCEPTION USING MESSAGE = $1`)
	return fmt.Sprintf(`CREATE OR REPLACE FUNCTION %s_fn() RETURNS trigger LANGUAGE plpgsql AS $fixture$ BEGIN %s RETURN NEW; END; $fixture$;
 CREATE TRIGGER %s %s %s ON %s FOR EACH ROW EXECUTE FUNCTION %s_fn()`, m[1], strings.TrimSpace(body), m[1], m[2], m[3], m[4], m[1])
}

func testDropTriggerSQL(name string) string {
	if postgresSubmissionTests() {
		return "DROP FUNCTION IF EXISTS " + name + "_fn() CASCADE"
	}
	return "DROP TRIGGER IF EXISTS " + name
}

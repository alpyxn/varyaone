#!/usr/bin/env python3
"""Tests for the database/unit package split.

The split decides which packages get a PostgreSQL server and which run in the
parallel batch. Getting it wrong is not loud: a misfiled integration package
joins the parallel run and races the others through PostgreSQL's shared lock
table, which surfaces as an unrelated flake somewhere else entirely. So the
classification rules are tested against a synthetic tree rather than trusted.

    python3 .github/scripts/db_test_groups_test.py
"""

import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import db_test_groups as split  # noqa: E402

MODULE = "example.test/app"


def tree(root: str, files: dict[str, str]) -> None:
    for relative, source in files.items():
        path = os.path.join(root, relative)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w", encoding="utf-8") as fh:
            fh.write(source)


def classify(files: dict[str, str]) -> set[str]:
    with tempfile.TemporaryDirectory() as root:
        tree(root, files)
        _, helpers, tests = split.scan(root, module=MODULE)
        return split.classify(tests, helpers)


class TestDetection(unittest.TestCase):
    def test_a_test_naming_the_variable_is_database_backed(self):
        found = classify(
            {
                "internal/sales/sales_test.go": (
                    'package sales\n\nfunc x() { os.Getenv("VARYAONE_TEST_DATABASE_URL") }\n'
                ),
            }
        )
        self.assertEqual(found, {"internal/sales"})

    def test_a_pure_unit_test_is_not(self):
        found = classify({"internal/money/money_test.go": "package money\n"})
        self.assertEqual(found, set())

    # The case the textual rule missed: a package that opens a database through
    # a shared helper never names the variable itself.
    def test_a_test_using_a_shared_helper_is_database_backed(self):
        found = classify(
            {
                "internal/testsupport/db.go": (
                    "package testsupport\n\n"
                    'const DSN = "VARYAONE_TEST_DATABASE_URL"\n'
                ),
                "internal/billing/billing_test.go": (
                    "package billing\n\n"
                    "import (\n"
                    '\t"testing"\n\n'
                    '\t"example.test/app/internal/testsupport"\n'
                    ")\n\n"
                    "func TestX(t *testing.T) { _ = testsupport.DSN }\n"
                ),
            }
        )
        self.assertIn("internal/billing", found)

    def test_a_single_line_import_of_a_helper_counts(self):
        found = classify(
            {
                "internal/testsupport/db.go": 'package testsupport\nconst D = "VARYAONE_TEST_DATABASE_URL"\n',
                "internal/billing/billing_test.go": (
                    "package billing\n"
                    'import "example.test/app/internal/testsupport"\n'
                ),
            }
        )
        self.assertIn("internal/billing", found)

    def test_an_aliased_import_of_a_helper_counts(self):
        found = classify(
            {
                "internal/testsupport/db.go": 'package testsupport\nconst D = "VARYAONE_TEST_DATABASE_URL"\n',
                "internal/billing/billing_test.go": (
                    "package billing\n\n"
                    "import (\n"
                    '\tsupport "example.test/app/internal/testsupport"\n'
                    ")\n"
                ),
            }
        )
        self.assertIn("internal/billing", found)

    # A package whose *tests* open a database is an integration package, not a
    # helper. Treating it as one files every importer of it as database-backed,
    # which is how a package touching no database at all ended up needing a
    # PostgreSQL server.
    def test_an_integration_package_is_not_a_helper(self):
        found = classify(
            {
                "internal/backup/engine_test.go": (
                    'package backup\nconst D = "VARYAONE_TEST_DATABASE_URL"\n'
                ),
                "internal/opctl/opctl_test.go": (
                    "package opctl\n\n"
                    "import (\n"
                    '\t"example.test/app/internal/backup"\n'
                    ")\n"
                ),
            }
        )
        self.assertEqual(found, {"internal/backup"})

    def test_non_test_files_never_join_the_list_on_their_own(self):
        # The helper itself has no tests, so it needs no server.
        found = classify(
            {
                "internal/testsupport/db.go": 'package testsupport\nconst D = "VARYAONE_TEST_DATABASE_URL"\n',
            }
        )
        self.assertEqual(found, set())


class TestImports(unittest.TestCase):
    def test_parses_blocks_singles_and_aliases(self):
        source = (
            "package x\n\n"
            "import (\n"
            '\t"fmt"\n'
            '\talias "example.test/app/internal/a"\n'
            '\t_ "example.test/app/internal/b"\n'
            ")\n"
            'import "example.test/app/internal/c"\n'
        )
        self.assertEqual(
            split.imports_of(source),
            {
                "fmt",
                "example.test/app/internal/a",
                "example.test/app/internal/b",
                "example.test/app/internal/c",
            },
        )


if __name__ == "__main__":
    unittest.main(verbosity=2)

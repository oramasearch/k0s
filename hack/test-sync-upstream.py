#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 k0s authors
# SPDX-License-Identifier: Apache-2.0
# Orama addition: exercise sync safeguards in disposable repositories.

import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent
UPSTREAM_URL = "https://github.com/k0sproject/k0s.git"


class SyncTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(
            prefix=".sync-test-", suffix=".tmp", dir=ROOT
        )
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.upstream = self.directory / "upstream"
        self.copy = self.directory / "copy"
        self.environment = os.environ.copy()
        self.environment.update(
            GIT_CONFIG_NOSYSTEM="1",
            GIT_CONFIG_GLOBAL=os.devnull,
            GIT_AUTHOR_NAME="Fixture",
            GIT_AUTHOR_EMAIL="fixture@example.invalid",
            GIT_COMMITTER_NAME="Fixture",
            GIT_COMMITTER_EMAIL="fixture@example.invalid",
            GIT_TERMINAL_PROMPT="0",
            GIT_EDITOR="true",
            GIT_SEQUENCE_EDITOR="true",
        )
        self.upstream.mkdir()
        self.git(self.upstream, "init", "-b", "main")
        self.write(self.upstream, "LICENSE", (ROOT / "LICENSE").read_text())
        self.write(self.upstream, "docs/LICENSE", (ROOT / "docs/LICENSE").read_text())
        self.write(self.upstream, "NOTICE", "Upstream attribution\n")
        self.write(self.upstream, "shared.txt", "upstream baseline\n")
        self.commit(self.upstream, "Upstream baseline")
        self.base = self.git(self.upstream, "rev-parse", "HEAD")
        self.git(self.upstream, "tag", "-a", "v1.0.0", "-m", "First release")
        self.git(self.directory, "clone", str(self.upstream), str(self.copy))
        self.git(self.copy, "switch", "-c", "liquid")
        self.record = (
            "**Last synced upstream tag:** `v1.0.0`\n\n"
            f"**Last synced upstream commit:** `{self.base}`\n"
        )
        self.write(
            self.copy,
            "ORAMA-FORK.md",
            "Copy modified to support liquid.\n" + self.record,
        )
        self.write(self.copy, "docs/liquid/upstream-sync.md", self.record)
        self.write(
            self.copy,
            "docs/liquid/README.md",
            "# Liquid-specific k0s changes\n\nSync tooling.\n",
        )
        shutil.copy(ROOT / "sync.sh", self.copy / "sync.sh")
        (self.copy / "hack").mkdir()
        shutil.copy(ROOT / "hack/sync-upstream.sh", self.copy / "hack/sync-upstream.sh")
        self.commit(self.copy, "Document private changes")
        self.git(self.copy, "remote", "add", "upstream", UPSTREAM_URL)
        self.git(self.copy, "remote", "set-url", "--push", "upstream", "DISABLED")
        self.git(self.copy, "config", f"url.{self.upstream}.insteadOf", UPSTREAM_URL)

    def run_command(self, directory, *command, success=True):
        result = subprocess.run(
            command,
            cwd=directory,
            env=self.environment,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            check=False,
        )
        if success:
            self.assertEqual(result.returncode, 0, result.stdout)
        return result

    def git(self, directory, *arguments):
        return self.run_command(directory, "git", *arguments).stdout.strip()

    def write(self, directory, filename, content):
        destination = directory / filename
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_text(content)

    def commit(self, directory, message):
        self.git(directory, "add", "--all")
        self.git(directory, "commit", "-m", message)

    def snapshot(self):
        return (
            self.git(self.copy, "show-ref", "--heads"),
            self.git(self.copy, "status", "--porcelain"),
            self.git(self.upstream, "show-ref"),
        )

    def sync(self, *arguments, success=True):
        return self.run_command(self.copy, "./sync.sh", *arguments, success=success)

    def advance_upstream(self, filename="new.txt", content="Upstream release\n"):
        self.write(self.upstream, filename, content)
        self.commit(self.upstream, "New upstream release")
        self.git(self.upstream, "tag", "v1.1.0")
        return self.git(self.upstream, "rev-parse", "HEAD")

    def assert_refusal(self, message, *arguments):
        before = self.snapshot()
        result = self.sync(*arguments, success=False)
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn(message, result.stdout)
        self.assertEqual(self.snapshot(), before)
        return result.stdout

    def test_dry_run_and_two_noop_runs(self):
        before = self.snapshot()
        for arguments in [("--dry-run",), (), ()]:
            result = self.sync(*arguments)
            self.assertIn("Upstream has not moved", result.stdout)
            self.assertIn(
                "LICENSE is present and byte-for-byte unchanged", result.stdout
            )
            self.assertIn(
                "NOTICE is present and byte-for-byte unchanged", result.stdout
            )
            self.assertEqual(self.snapshot(), before)
            print(f"\n$ ./sync.sh {' '.join(arguments)}\n{result.stdout}", flush=True)

    def test_fast_forward_rebase_and_repeat(self):
        target = self.advance_upstream()
        before = self.snapshot()
        self.assertIn("Would fast-forward", self.sync("--dry-run").stdout)
        self.assertEqual(self.snapshot(), before)
        result = self.sync()
        self.assertIn("Fast-forwarded main", result.stdout)
        self.assertEqual(self.git(self.copy, "rev-parse", "main"), target)
        self.git(self.copy, "merge-base", "--is-ancestor", "main", "liquid")
        self.assertEqual((self.copy / "new.txt").read_text(), "Upstream release\n")
        for filename in ["ORAMA-FORK.md", "docs/liquid/upstream-sync.md"]:
            record = (self.copy / filename).read_text()
            self.assertIn("`v1.1.0`", record)
            self.assertIn(f"`{target}`", record)
        changed = self.git(
            self.copy, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD"
        )
        self.assertEqual(
            changed.splitlines(), ["ORAMA-FORK.md", "docs/liquid/upstream-sync.md"]
        )
        before_repeat = self.snapshot()
        self.assertIn("Nothing to do", self.sync().stdout)
        self.assertEqual(self.snapshot(), before_repeat)

    def test_commit_on_main_is_refused_in_both_modes(self):
        self.git(self.copy, "switch", "main")
        self.write(self.copy, "dependabot.txt", "Local mirror drift\n")
        self.commit(self.copy, "Merge Dependabot update into mirror")
        self.git(self.copy, "switch", "liquid")
        for arguments in [("--dry-run",), ()]:
            output = self.assert_refusal("main cannot fast-forward", *arguments)
            self.assertIn("likely a merged Dependabot PR", output)
            self.assertIn("Merge Dependabot update into mirror", output)
            print(
                f"\n$ ./sync.sh {' '.join(arguments)} # polluted scratch main\n{output}",
                flush=True,
            )

    def test_cached_conflict_resolution_is_not_reused(self):
        self.write(self.copy, "shared.txt", "Private choice\n")
        self.commit(self.copy, "Private conflicting change")
        self.advance_upstream("shared.txt", "Upstream conflicting change\n")
        self.git(self.copy, "config", "rerere.enabled", "true")
        self.git(self.copy, "config", "rerere.autoupdate", "true")
        self.git(self.copy, "fetch", "upstream", "main")
        before = self.git(self.copy, "rev-parse", "liquid")
        conflict = self.run_command(
            self.copy, "git", "rebase", "FETCH_HEAD", success=False
        )
        self.assertNotEqual(conflict.returncode, 0)
        self.write(self.copy, "shared.txt", "Previously cached resolution\n")
        self.git(self.copy, "add", "shared.txt")
        self.git(self.copy, "rebase", "--continue")
        self.git(self.copy, "reset", "--hard", before)
        result = self.sync(success=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Rebase stopped; conflicting paths", result.stdout)
        self.assertIn("shared.txt", result.stdout)
        self.assertEqual(
            self.git(self.copy, "diff", "--name-only", "--diff-filter=U"), "shared.txt"
        )
        self.assertNotIn(
            "Previously cached resolution", (self.copy / "shared.txt").read_text()
        )
        self.assertIn("<<<<<<<", (self.copy / "shared.txt").read_text())
        self.assertEqual(
            (self.copy / "docs/liquid/upstream-sync.md").read_text(), self.record
        )
        self.git(self.copy, "rebase", "--abort")
        self.assertEqual(self.git(self.copy, "rev-parse", "liquid"), before)
        self.assertNotEqual(self.sync(success=False).returncode, 0)
        print(
            f"\n$ ./sync.sh # conflicting scratch branches, rerere enabled\n{result.stdout}",
            flush=True,
        )

    def test_licence_tampering_is_refused(self):
        for filename in ["LICENSE", "NOTICE", "docs/LICENSE"]:
            with self.subTest(filename=filename):
                self.write(self.copy, filename, "Changed attribution\n")
                self.commit(self.copy, f"Alter {filename}")
                self.assert_refusal(f"liquid:{filename} differs", "--dry-run")
                self.assert_refusal(f"liquid:{filename} differs")
                self.git(self.copy, "reset", "--hard", "HEAD~1")

    def test_missing_license_is_refused(self):
        (self.copy / "LICENSE").unlink()
        self.commit(self.copy, "Remove licence")
        self.assert_refusal("liquid must retain LICENSE")

    def test_absent_upstream_notice_is_reported(self):
        self.git(self.upstream, "rm", "NOTICE")
        self.commit(self.upstream, "Remove root notice upstream")
        self.git(self.copy, "fetch", "upstream", "main")
        self.git(self.copy, "rebase", "FETCH_HEAD")
        self.git(self.copy, "branch", "-f", "main", "FETCH_HEAD")
        self.assertIn("no root NOTICE", self.sync().stdout)

    def test_new_upstream_notice_is_preserved(self):
        self.advance_upstream("NOTICE", "Updated upstream attribution\n")
        self.sync()
        self.assertEqual(
            (self.copy / "NOTICE").read_text(), "Updated upstream attribution\n"
        )

    def test_placeholder_and_unrelated_tag_are_refused(self):
        policy = (self.copy / "ORAMA-FORK.md").read_text()
        self.write(
            self.copy, "ORAMA-FORK.md", policy.replace("`v1.0.0`", "_not yet recorded_")
        )
        self.commit(self.copy, "Leave placeholder baseline")
        self.assert_refusal("tag is missing or still a placeholder", "--dry-run")
        self.git(self.copy, "reset", "--hard", "HEAD~1")
        self.git(self.upstream, "switch", "-c", "future")
        self.write(self.upstream, "future.txt", "Unmerged release\n")
        self.commit(self.upstream, "Unrelated future release")
        self.git(self.upstream, "tag", "v99.0.0")
        self.git(self.upstream, "switch", "main")
        self.assertIn("New upstream tag: v1.0.0", self.sync("--dry-run").stdout)
        self.write(self.copy, "ORAMA-FORK.md", policy.replace("`v1.0.0`", "`v99.0.0`"))
        self.commit(self.copy, "Claim unrelated release")
        self.assert_refusal("recorded tag is not an ancestor", "--dry-run")

    def test_local_tags_cannot_impersonate_upstream(self):
        self.git(self.copy, "tag", "-f", "v1.0.0", "liquid")
        self.git(self.copy, "tag", "v999.0.0")
        self.assertIn("New upstream tag: v1.0.0", self.sync("--dry-run").stdout)

    def test_unsafe_push_url_and_push_option_are_refused(self):
        self.git(self.copy, "config", "--add", "remote.upstream.pushurl", UPSTREAM_URL)
        self.assert_refusal("push URL DISABLED", "--dry-run")
        self.assert_refusal("unknown option: --push", "--push")

    def test_dirty_checkout_and_custom_driver_are_refused(self):
        self.write(self.copy, "untracked.txt", "Keep this file\n")
        self.assert_refusal("checkout must be clean")
        (self.copy / "untracked.txt").unlink()
        self.git(self.copy, "config", "merge.custom.driver", "true")
        self.assert_refusal("custom merge drivers require human review")

    def test_tag_selection_does_not_rewind_main(self):
        self.advance_upstream()
        self.sync("--tag", "v1.1.0")
        self.assert_refusal("main cannot fast-forward", "--tag", "v1.0.0")

    def test_union_merge_driver_is_refused(self):
        self.write(self.copy, ".gitattributes", "shared.txt merge=union\n")
        self.commit(self.copy, "Enable union merging")
        self.assert_refusal("uses merge=union", "--dry-run")
        self.assert_refusal("uses merge=union")

    def test_inconsistent_guide_record_is_refused_before_fast_forward(self):
        self.advance_upstream()
        self.write(self.copy, "docs/liquid/upstream-sync.md", "Missing record\n")
        self.commit(self.copy, "Break guide record")
        self.assert_refusal("must record the same upstream tag")


if __name__ == "__main__":
    unittest.main(verbosity=2)

"""Offline coverage for the image queue, registry index and progress display."""

import hashlib
import json
import subprocess
import tempfile
import unittest
from contextlib import redirect_stdout
from io import StringIO
from pathlib import Path
from unittest.mock import MagicMock, patch

from image_index import SizeIndex, generate_index
from pull_images import download, image_queue
from pull_progress import QueueMeter, WindowRate


class ImageTests(unittest.TestCase):
    """Exercise the downloader without a Docker daemon or registry connection."""

    def test_roles_and_wal(self) -> None:
        """Deduplicate shared references and honor role and WAL filters."""
        agent = "example/agent@sha256:" + "1" * 64
        verifier = "example/check@sha256:" + "2" * 64
        with tempfile.TemporaryDirectory() as directory:
            dataset = Path(directory)
            for i in range(63):
                name = "wal-recovery-ordering" if i == 62 else str(i)
                folder = dataset / name
                folder.mkdir()
                image = verifier if i == 62 else agent
                (folder / "task.toml").write_text(
                    f'[task]\nname="{name}"\n[environment]\ndocker_image="{image}"\n'
                    f'[verifier.environment]\ndocker_image="{verifier}"\n'
                )
            self.assertEqual(
                image_queue(dataset=dataset, role="agent", include_wal=False), [agent]
            )
            self.assertEqual(
                image_queue(dataset=dataset, role="agent", include_wal=True),
                [agent, verifier],
            )
            self.assertEqual(
                image_queue(dataset=dataset, role="verifier", include_wal=False),
                [verifier],
            )

    def test_index(self) -> None:
        """Select the amd64 child, verify raw bytes and preserve the previous index on failure."""
        config = "sha256:" + "a" * 64
        layer = "b" * 64
        child = json.dumps(
            {
                "config": {"digest": config, "size": 10},
                "layers": [{"digest": "sha256:" + layer, "size": 12}],
            }
        ).encode()
        child_id = "sha256:" + hashlib.sha256(child).hexdigest()
        parent = json.dumps(
            {
                "manifests": [
                    {
                        "digest": child_id,
                        "size": len(child),
                        "platform": {"os": "linux", "architecture": "amd64"},
                    }
                ]
            }
        ).encode()
        digest = "sha256:" + hashlib.sha256(parent).hexdigest()
        refs = ["example/task@" + digest]
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / "sizes.json"
            with patch(
                "image_index.subprocess.check_output",
                side_effect=[parent, child + b"\n"],
            ) as fetch:
                generate_index(refs=refs, path=target)
                self.assertIn("example/task@" + child_id, fetch.call_args.args[0])
            data = target.read_bytes()
            index = SizeIndex.model_validate_json(data)
            self.assertEqual(index.images[digest], (config, [layer]))
            self.assertEqual(index.sizes, {layer: 12})
            with (
                patch("image_index.subprocess.check_output", return_value=b"{}"),
                self.assertRaisesRegex(ValueError, "digest mismatch"),
            ):
                generate_index(refs=refs, path=target)
            self.assertEqual(target.read_bytes(), data)

    def test_progress(self) -> None:
        """Keep shared bytes, cache hits, retries and terminal redraws distinct."""
        a, b = "a" * 64, "b" * 64
        idx = SizeIndex(
            images={"one": ("cfg1", [a]), "two": ("cfg2", [a, b])},
            sizes={a: 500, b: 1000},
        )
        with (
            patch("pull_progress.SizeIndex.model_validate_json", return_value=idx),
            patch(
                "pull_progress.subprocess.run",
                return_value=subprocess.CompletedProcess([], 0, stdout=""),
            ),
        ):
            meter = QueueMeter(refs=["r@one", "r@two"], cached={"r@one"}, window=60)
        self.assertEqual(meter.remaining(), 1000)
        meter.begin(ref="r@two")
        meter.line(line=a[:12] + ": Already exists")
        meter.line(line=b[:12] + ": Downloading 400B/1kB")
        self.assertEqual((meter.remaining(), meter.total_wire), (600, 400))
        terminal = StringIO()
        with (
            redirect_stdout(terminal),
            patch.object(terminal, "isatty", return_value=True),
        ):
            meter.last_print = float("-inf")
            meter.show()
            meter.last_print = float("-inf")
            meter.show()
        self.assertIn("\r\x1b[2K", terminal.getvalue())
        self.assertNotIn("\n", terminal.getvalue())
        meter.begin(ref="r@two")
        self.assertEqual(meter.remaining(), 1000)
        meter.commit(ref="r@two")
        self.assertEqual(meter.remaining(), 0)

    def test_rate(self) -> None:
        """Interpolate the window boundary and age idle transfers out."""
        rate = WindowRate(seconds=60, now=0)
        rate.sample(now=20, total=200)
        self.assertEqual(rate.sample(now=70, total=1000), 15)
        self.assertEqual(rate.sample(now=130, total=1000), 0)

    def test_mirror_does_not_replace_origin(self) -> None:
        """Verify the original digest and defer retry of a failed origin."""
        a, b = "r:a@sha256:aa", "r:b@sha256:bb"
        with (
            patch("pull_images.cached_refs", return_value=set()),
            patch("pull_images.QueueMeter", return_value=MagicMock()),
            patch("pull_images.present", return_value=False),
            patch("pull_images.time.sleep"),
            patch(
                "pull_images.pull_once", side_effect=[True, False, True, True, True]
            ) as pull,
            redirect_stdout(StringIO()),
        ):
            self.assertEqual(
                download(
                    refs=[a, b],
                    retries=1,
                    timeout=0,
                    mirror="mirror.example",
                    window=60,
                ),
                0,
            )
        self.assertEqual(
            [c.kwargs["ref"] for c in pull.call_args_list],
            ["mirror.example/r@sha256:aa", a, "mirror.example/r@sha256:bb", b, a],
        )

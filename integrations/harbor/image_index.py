"""Generate the compressed layer size index for pinned linux/amd64 images."""

import hashlib
import json
import os
import subprocess
import tempfile
from pathlib import Path
from typing import Annotated

from pydantic import BaseModel, Field, NonNegativeInt

Digest = Annotated[str, Field(pattern=r"^sha256:[0-9a-f]{64}$")]
INDEX = Path(__file__).with_name("pull_image_sizes.json")


class SizeIndex(BaseModel):
    """Map pinned manifests to a config digest and ordered compressed layer IDs."""

    images: dict[str, tuple[str, list[str]]]
    sizes: dict[str, int]


class Descriptor(BaseModel):
    """Registry descriptor for a content-addressed object."""

    digest: Digest
    size: NonNegativeInt


class PlatformManifest(Descriptor):
    """One platform-specific manifest in a registry image index."""

    platform: dict[str, str | list[str]] = {}


class ImageManifest(BaseModel):
    """Registry properties needed to resolve a single image."""

    config: Descriptor
    layers: list[Descriptor]


class PlatformIndex(BaseModel):
    """Docker manifest list or OCI image index."""

    manifests: list[PlatformManifest]


def _raw_manifest(*, ref: str) -> bytes:
    raw = subprocess.check_output(
        ["docker", "buildx", "imagetools", "inspect", "--raw", ref], timeout=90
    )
    digest = ref.rsplit("@sha256:", 1)[1]
    for candidate in (raw, raw.removesuffix(b"\n")):
        if hashlib.sha256(candidate).hexdigest() == digest:
            return candidate
    raise ValueError(f"Manifest digest mismatch for {ref}")


def generate_index(*, refs: list[str], path: Path) -> None:
    """Verify registry manifests and atomically replace the completed index.

    :param refs: Docker image references pinned by digest.
    :param path: Destination for the generated JSON file.
    :raises ValueError: A manifest is invalid or lacks a unique linux/amd64 image.
    """
    index = SizeIndex(images={}, sizes={})
    for ref in refs:
        name, digest = ref.rsplit("@", 1)
        if digest in index.images:
            continue
        raw = _raw_manifest(ref=ref)
        if "manifests" in json.loads(raw):
            matches = [
                entry.digest
                for entry in PlatformIndex.model_validate_json(raw).manifests
                if entry.platform.get("os") == "linux"
                and entry.platform.get("architecture") == "amd64"
            ]
            if len(matches) != 1:
                raise ValueError(f"Expected one linux/amd64 manifest for {ref}")
            raw = _raw_manifest(ref=f"{name}@{matches[0]}")
        manifest = ImageManifest.model_validate_json(raw)
        layers: list[str] = []
        for layer in manifest.layers:
            key = layer.digest.removeprefix("sha256:")
            if index.sizes.setdefault(key, layer.size) != layer.size:
                raise ValueError(f"Conflicting sizes for {layer.digest}")
            layers.append(key)
        index.images[digest] = (manifest.config.digest, layers)
    content = json.dumps(index.model_dump(), indent=2, sort_keys=True) + "\n"
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as tmp:
        try:
            tmp.write(content.encode())
            tmp.flush()
            os.replace(tmp.name, path)
        finally:
            Path(tmp.name).unlink(missing_ok=True)

"""Pinned Hint build identity shared by the source builder and Harbor adapter.

Executing this file prints the version and full commit, separated by a space.
The source builder uses this dependency-free entry point before installation.
"""

from typing import Final

HINT_VERSION: Final = "0.4.3"
REVISION: Final = "01440155d1442d78354990d54ce6b0fde2d79fd9"


if __name__ == "__main__":
    print(HINT_VERSION, REVISION)

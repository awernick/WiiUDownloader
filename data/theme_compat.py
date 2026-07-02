from __future__ import annotations

import os
import shutil
from pathlib import Path


def install_adwaita_compat_aliases(icon_theme_dir: str) -> None:
    """Add compatibility icon aliases expected by the GTK runtime."""
    actions_dir = Path(icon_theme_dir) / "symbolic" / "actions"
    if not actions_dir.is_dir():
        return

    aliases = [
        ("view-list-bullet-symbolic.svg", "bullet-symbolic.svg"),
        ("view-list-bullet-symbolic-rtl.svg", "bullet-symbolic-rtl.svg"),
    ]

    for source_name, alias_name in aliases:
        source = actions_dir / source_name
        alias = actions_dir / alias_name
        if source.exists() and not alias.exists():
            shutil.copy2(source, alias)


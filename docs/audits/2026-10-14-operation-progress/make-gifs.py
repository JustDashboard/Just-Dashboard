"""Convert the opted-in browser recordings to reviewable GIFs; requires Pillow."""

import argparse
from pathlib import Path

from PIL import Image


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("frames", type=Path)
    args = parser.parse_args()
    output = Path(__file__).parent
    for folder in sorted(args.frames.iterdir()):
        paths = sorted(folder.glob("*.png"))
        if len(paths) != 22:
            print(f"Skipping incomplete recording: {folder.name} ({len(paths)} frames)")
            continue
        originals = [Image.open(path).convert("RGB") for path in paths]
        palette = originals[1].quantize(colors=256)
        frames = [frame.quantize(palette=palette, dither=Image.Dither.NONE) for frame in originals]
        target = output / f"{folder.name}.gif"
        frames[0].save(
            target,
            save_all=True,
            append_images=frames[1:],
            duration=[700] + [200] * (len(frames) - 2) + [1300],
            loop=0,
            optimize=True,
        )
        print(f"{target.name}: {target.stat().st_size:,} bytes")


if __name__ == "__main__":
    main()

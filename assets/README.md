# Egg icon

`source-logo.png` is the 512 × 512 panel icon: the square Source engine symbol (white **S** with orange arc), on a transparent background. Its PNG bytes are embedded in the egg export's `icon` field, so Pelican imports it without fetching an external image URL.

`source-logo.svg` preserves the square vector artwork from [Devicon](https://github.com/devicons/devicon/blob/master/icons/sourceengine/sourceengine-original.svg), whose collection is MIT licensed; see [LICENSE.devicon](LICENSE.devicon). The Source branding belongs to Valve; this asset is not part of the project's original code artwork.

`edgegamers-mark.webp` is the EdgeGamers mark supplied for this project's installer banner. EdgeGamers retains ownership of its branding.

The wizard renders both marks as ordinary Unicode half-block text, with orange Source accents when color is supported. No image library or terminal image protocol is required at runtime. The text is embedded in `internal/terminalui/logos.go`; regenerate it with `python3 scripts/generate-terminal-art.py` using Pillow. Narrow terminals stack the marks; ASCII mode uses their names.

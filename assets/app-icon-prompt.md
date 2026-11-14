# App icon rendering

The icon is hand-drawn as a precise SVG in `assets/app-icon.svg`, following the geometry,
colors, and deliverables in `assets/icon-design.md`. Edit the SVG directly; no image
generation model is used.

From the repository root, render the 1024 × 1024 PNG and the 256 × 256 About icon:

```sh
rsvg-convert -w 1024 -h 1024 assets/app-icon.svg -o assets/app-icon.png
sips -Z 256 assets/app-icon.png --out desktopui/about-icon.png
```

The canvas stays transparent around the rounded tile and its soft shadow. The avatars
and active-account dot are translated together by (-17, -24) for optical centering.

Render small previews into a temporary directory and inspect both for legibility:

```sh
icon_previews=$(mktemp -d)
rsvg-convert -w 32 -h 32 assets/app-icon.svg -o "$icon_previews/app-32.png"
rsvg-convert -w 64 -h 64 assets/app-icon.svg -o "$icon_previews/app-64.png"
```

The monochrome menu-bar template is drawn separately by `trayIcon()` in
`desktopui/tray.go`, using vector alpha masks for the overlapping avatars and
transparent head-and-shoulders cutout.

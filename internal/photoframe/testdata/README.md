# Renderer fixtures

- `solid-red.webp`: generated locally as a lossless 1×1 red image; contains no user media.
- `libvips-srgb.icm`: the complete 480-byte standard sRGB profile from
  [libvips v8.17.3](https://github.com/libvips/libvips/blob/v8.17.3/libvips/colour/profiles/sRGB.icm).
  Its embedded copyright tag is `CC0`. SHA-256:
  `c56e1685d888f5edb92fe07f2750f387f8fe8e91b32ff8fb0b56bfbbb9458353`.
  It is used to distinguish known sRGB from arbitrary or malformed ICC profiles.

- `libvips-p3.icm`: the 480-byte Display P3 profile (`sP3C`) from the same
  [libvips release](https://github.com/libvips/libvips/blob/v8.17.3/libvips/colour/profiles/p3.icm).
  Its embedded copyright tag is `CC0`. SHA-256:
  `231752984cd4a5278e1b8d2390fe496767d4511fc81f54e1a5c69ae9ab4c42b5`.
  The P3→sRGB matrix is derived from these two profiles' D50-adapted RGB colorants;
  their transfer curves are identical. Out-of-gamut channels are clipped.

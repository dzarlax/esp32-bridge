import fixture from "./voice-pairing-qr.json";

export const pairingPayload = fixture.payload;

export function qrFrame(inverted = false) {
  const scale = 4;
  const quietZone = 4;
  const width = (fixture.modules.length + quietZone * 2) * scale;
  const data = new Uint8ClampedArray(width * width * 4);
  for (let y = 0; y < width; y++) {
    for (let x = 0; x < width; x++) {
      const row = Math.floor(y / scale) - quietZone;
      const column = Math.floor(x / scale) - quietZone;
      const dark = fixture.modules[row]?.[column] === "1";
      const value = dark !== inverted ? 0 : 255;
      const offset = (y * width + x) * 4;
      data[offset] = data[offset + 1] = data[offset + 2] = value;
      data[offset + 3] = 255;
    }
  }
  return { data, width, height: width };
}

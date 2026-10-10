import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const styles = await readFile(new URL('../src/styles.css', import.meta.url), 'utf8');
const tokens = Object.fromEntries([...styles.matchAll(/--([a-z-]+):\s*(#[a-f\d]{6});/gi)].map(([, name, value]) => [name, value]));
const luminance = (hex) => {
  const channels = hex.slice(1).match(/../g).map((part) => parseInt(part, 16) / 255).map((value) => value <= .04045 ? value / 12.92 : ((value + .055) / 1.055) ** 2.4);
  return channels[0] * .2126 + channels[1] * .7152 + channels[2] * .0722;
};

test('responsive typography never uses sub-13px text to squeeze the layout', () => {
  const sizes = [...styles.matchAll(/font(?:-size)?:\s*([\d.]+)px/g)].map(([, value]) => Number(value));
  assert.ok(sizes.length > 30);
  assert.ok(sizes.every((size) => size >= 13));
  assert.match(styles, /font-size:\s*16px;/);
  assert.doesNotMatch(styles, /zoom\s*:|transform\s*:\s*scale\(\.[1-8]\)/);
});

test('text and status colors meet normal-text AA contrast on every base surface', () => {
  for (const foreground of ['text', 'secondary', 'muted', 'faint', 'green', 'amber', 'red', 'blue']) {
    for (const background of ['bg', 'sidebar', 'surface', 'surface-raised', 'surface-hover']) {
      const values = [luminance(tokens[foreground]), luminance(tokens[background])].sort((a, b) => b - a);
      assert.ok((values[0] + .05) / (values[1] + .05) >= 4.5, `${foreground} on ${background}`);
    }
  }
});

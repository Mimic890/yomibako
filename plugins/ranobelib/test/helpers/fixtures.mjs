import { readFile } from 'node:fs/promises';

const FIXTURES = new URL('../fixtures/', import.meta.url);

export async function fixtureJson(name) {
  return JSON.parse(await readFile(new URL(name, FIXTURES), 'utf8'));
}

export async function fixtureBytes(name) {
  return new Uint8Array(await readFile(new URL(name, FIXTURES)));
}

export function fixtureUrl(name) {
  return new URL(name, FIXTURES);
}

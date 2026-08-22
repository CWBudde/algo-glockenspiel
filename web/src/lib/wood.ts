import beechTexture from "../../assets/wood/beech.png";
import mapleTexture from "../../assets/wood/maple.png";
import oakTexture from "../../assets/wood/oak.png";
import walnutTexture from "../../assets/wood/walnut.png";
import presets from "./wood-presets.json";

export interface WoodSpeciesOption {
  id: string;
  label: string;
  description: string;
}

const TEXTURES: Record<string, string> = {
  beech: beechTexture,
  walnut: walnutTexture,
  oak: oakTexture,
  maple: mapleTexture,
};

const speciesEntries = Object.entries(presets.species);

export function getWoodSpeciesOptions(): WoodSpeciesOption[] {
  return speciesEntries.map(([id, preset]) => ({
    id,
    label: preset.name,
    description: preset.description,
  }));
}

export function applyWoodTexture(
  root: HTMLElement | null = null,
  species: string = presets.defaultSpecies,
): void {
  const target =
    root ?? (typeof document === "undefined" ? null : document.documentElement);
  if (!target) {
    return;
  }

  const resolvedSpecies = TEXTURES[species] ? species : presets.defaultSpecies;
  target.style.setProperty(
    "--wood-panel-texture",
    `url("${TEXTURES[resolvedSpecies]}")`,
  );
  target.dataset.woodSpecies = resolvedSpecies;
}

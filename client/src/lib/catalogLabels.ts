import { uiText } from '../locales/ui'
import { defaultStatusNames } from '../locales/ja/ui'

// Built-in workflow names are copied into each project. Match both key and seed name so
// project-owned names remain exactly as entered by their owners.
export function statusLabel(key: string, name: string): string {
  return defaultStatusNames[key] === name ? uiText(name) : name
}

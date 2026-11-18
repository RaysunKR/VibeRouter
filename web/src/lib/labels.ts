import type { Model, Provider } from './api'

export function providerLabel(provider: Provider): string {
  return provider.name === provider.id ? provider.name : `${provider.name} (${provider.id})`
}

export function providerLabelFor(providers: Provider[], providerID: string): string {
  const provider = providers.find((p) => p.id === providerID)
  return provider ? providerLabel(provider) : providerID
}

export function modelProviderLabel(model: Model, providers: Provider[]): string {
  const provider = providers.find((p) => p.id === model.provider)
  return provider ? `${model.name} · ${providerLabel(provider)}` : model.name
}

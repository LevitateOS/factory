/** Product transport supplied by a native desktop or Fountain adapter. */
export interface FactoryTransport {
  readonly generation: string;
  readonly assetBase: string;
  request(path: string, init?: RequestInit): Promise<Response>;
}

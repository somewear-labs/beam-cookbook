import protobuf from 'protobufjs';
import { app } from 'electron';
import path from 'path';

// In production app.getAppPath() is Resources/app.asar, so .. → Resources/,
// where rpc is bundled as beam-cookbook/rpc/. In dev mode it's the project
// root (examples/beam-ops), so we walk up three levels to the cookbook root.
const PROTO_ROOT = app.isPackaged
  ? path.join(path.resolve(app.getAppPath(), '..'), 'beam-cookbook')
  : path.resolve(app.getAppPath(), '..', '..', '..');

// Maps payload type names to their proto file and fully-qualified message name.
// IPv4Datagram carries an RPC Envelope from the cookbook. All other types that
// need deserialization here should use SDK protos from swl-proto.
const TYPE_PROTO_MAP: Record<string, { file: string; message: string }> = {
  IPv4Datagram: {
    file: path.join(PROTO_ROOT, 'rpc', 'proto', 'rpc.proto'),
    message: 'somewear.rpc.Envelope',
  },
};

const rootCache = new Map<string, protobuf.Root>();

async function loadRoot(filePath: string): Promise<protobuf.Root> {
  const cached = rootCache.get(filePath);
  if (cached) return cached;
  const root = await protobuf.load(filePath);
  rootCache.set(filePath, root);
  return root;
}

export async function deserializeContent(
  type: string,
  contentBytesB64: string
): Promise<unknown | null> {
  const mapping = TYPE_PROTO_MAP[type];
  if (!mapping) return null;

  try {
    const bytes = Buffer.from(contentBytesB64, 'base64');
    const root = await loadRoot(mapping.file);
    const MessageType = root.lookupType(mapping.message);
    const decoded = MessageType.decode(bytes);
    return MessageType.toObject(decoded, {
      defaults: false,
      longs: String,
      enums: String,
      bytes: String,
    });
  } catch (err) {
    console.warn(`[Proto] Failed to deserialize ${type}:`, err);
    return null;
  }
}

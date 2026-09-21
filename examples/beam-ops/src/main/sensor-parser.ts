import protobuf from 'protobufjs';
import { app } from 'electron';
import path from 'path';

// Wire format: bytes 0-2 = 'SWL' magic, byte 3 = sensor type, bytes 4+ = protobuf payload.

const PROJECT_ROOT = path.resolve(app.getAppPath(), '..');
const SENSORS_PROTO = path.join(PROJECT_ROOT, 'beam-cookbook', 'sensors', 'proto', 'sensors.proto');

export const SWL_SENSOR_TYPES: Record<number, string> = {
  1: 'UGS',
  2: 'CBRN',
  3: 'TWS',
  4: 'VIMU',
  5: 'SEISMOGRAPH',
};

const TYPE_MESSAGE: Record<number, string> = {
  1: 'sensors.UGSData',
  2: 'sensors.CBRNData',
  3: 'sensors.TacticalWeather',
  4: 'sensors.VehicleIMUData',
  5: 'sensors.SeismicData',
};

export interface SensorPayload {
  sensorType: number;
  sensorName: string;
  sensorData: unknown;
}

let rootPromise: Promise<protobuf.Root> | null = null;

function getRoot(): Promise<protobuf.Root> {
  if (!rootPromise) rootPromise = protobuf.load(SENSORS_PROTO);
  return rootPromise;
}

export async function parseSensorPayload(b64: string): Promise<SensorPayload | null> {
  let buf: Buffer;
  try {
    buf = Buffer.from(b64, 'base64');
  } catch {
    return null;
  }

  if (buf.length < 5) return null;
  if (buf[0] !== 0x53 || buf[1] !== 0x57 || buf[2] !== 0x4c) return null; // 'SWL'

  const typeNum = buf[3];
  const sensorName = SWL_SENSOR_TYPES[typeNum] ?? `SENSOR_TYPE_${typeNum}`;
  const messageName = TYPE_MESSAGE[typeNum];
  if (!messageName) return null;

  try {
    const root = await getRoot();
    const MessageType = root.lookupType(messageName);
    const decoded = MessageType.decode(buf.slice(4));
    const sensorData = MessageType.toObject(decoded, {
      defaults: false,
      longs: String,
      enums: String,
      bytes: String,
    });
    return { sensorType: typeNum, sensorName, sensorData };
  } catch (err) {
    console.warn('[SensorParser] Failed to decode sensor type', typeNum, ':', err);
    return null;
  }
}

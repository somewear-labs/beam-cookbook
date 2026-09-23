import protobuf from 'protobufjs';
import { app } from 'electron';
import path from 'path';

// Wire format: bytes 0-2 = 'SWL' magic, byte 3 = sensor type, bytes 4+ = protobuf payload.

// Dev:      beam-cookbook/examples/beam-ops → ../../ → beam-cookbook root
// Packaged: Resources/ + beam-cookbook
const COOKBOOK_ROOT = app.isPackaged
  ? path.join(path.resolve(app.getAppPath(), '..'), 'beam-cookbook')
  : path.resolve(app.getAppPath(), '..', '..');
const SENSORS_PROTO = path.join(COOKBOOK_ROOT, 'sensors', 'proto', 'sensors.proto');

export const SWL_SENSOR_TYPES: Record<number, string> = {
  1: 'UGS',
  2: 'CBRN',
  3: 'TWS',
  4: 'VIMU',
  5: 'SEISMOGRAPH',
  7: 'SYSMON',
};

const TYPE_MESSAGE: Record<number, string> = {
  1: 'sensors.UGSData',
  2: 'sensors.CBRNData',
  3: 'sensors.TacticalWeather',
  4: 'sensors.VehicleIMUData',
  5: 'sensors.SeismicData',
  7: 'sensors.ComputerDiagnosticsData',
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

  const payload = buf.slice(4);

  try {
    const root = await getRoot();
    const MessageType = root.lookupType(messageName);
    const decoded = MessageType.decode(payload);
    const sensorData = MessageType.toObject(decoded, {
      defaults: false,
      longs: String,
      enums: String,
      bytes: String,
    });
    return { sensorType: typeNum, sensorName, sensorData };
  } catch {
    // Fall back to JSON — simulator tools send JSON instead of protobuf.
  }

  try {
    const sensorData = JSON.parse(payload.toString('utf8'));
    return { sensorType: typeNum, sensorName, sensorData };
  } catch {
    return null;
  }
}

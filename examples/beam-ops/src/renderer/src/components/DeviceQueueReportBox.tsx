import { useEffect, useState } from 'react';
import { beamApi, DeviceQueueReport } from '../services/beamApi';

export default function DeviceQueueReportBox() {
  const [report, setReport] = useState<DeviceQueueReport | null>(null);

  useEffect(() => {
    const cleanup = beamApi.tailDeviceQueueReport((r) => {
      setReport(r.hasReport ? r : null);
    });
    return cleanup;
  }, []);

  return (
    <div className="queue-report-box">
      <div className="queue-report-title">DEVICE QUEUE</div>
      {report ? (
        <>
          <div className="queue-report-row">
            <span className="queue-report-label">USED</span>
            <span className="queue-report-value">{report.utilization}/{report.capacity}</span>
          </div>
          <div className="queue-report-row">
            <span className="queue-report-label">FREE</span>
            <span className={`queue-report-value ${report.freeSlots === 0 ? 'queue-report-full' : ''}`}>
              {report.freeSlots}
            </span>
          </div>
          <div className="queue-report-divider" />
          <div className="queue-report-row">
            <span className="queue-report-label">SAT</span>
            <span className="queue-report-value">{report.satCount}</span>
          </div>
          <div className="queue-report-row">
            <span className="queue-report-label">RADIO</span>
            <span className="queue-report-value">{report.radioCount}</span>
          </div>
          <div className="queue-report-row">
            <span className="queue-report-label">CELL</span>
            <span className="queue-report-value">{report.cellCount}</span>
          </div>
          {report.backhaulCount > 0 && (
            <div className="queue-report-row">
              <span className="queue-report-label">BKH</span>
              <span className="queue-report-value">{report.backhaulCount}</span>
            </div>
          )}
        </>
      ) : (
        <div className="queue-report-empty">—</div>
      )}
    </div>
  );
}

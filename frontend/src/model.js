// 纯函数模型层：不依赖 DOM、不发起网络请求、不修改输入快照。
// 该层负责“判定语义”：数据过期 => 未知、统计分母、可用率与降级比例、延迟格式化。
// 全部为演示可用的纯逻辑，不构成对真实设备/业务的健康结论。

export const EXCLUDED_FROM_UPTIME = ['unknown', 'untested', 'maintenance'];

const STATUSES = ['healthy', 'degraded', 'unknown', 'untested', 'maintenance', 'down'];

function toMillis(value) {
  if (value === null || value === undefined) return null;
  if (typeof value === 'number') return Number.isFinite(value) ? value : null;
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? parsed : null;
}

function round(value, digits) {
  const factor = 10 ** digits;
  return Math.round(value * factor) / factor;
}

/**
 * 格式化延迟。null/undefined/NaN/负数/非数值 全部安全返回占位符。
 * @param {number|null|undefined} ms
 * @returns {string}
 */
export function formatLatency(ms) {
  if (ms === null || ms === undefined) return '—';
  const value = Number(ms);
  if (!Number.isFinite(value) || value < 0) return '—';
  if (value < 1000) return `${Math.round(value)} ms`;
  return `${(value / 1000).toFixed(2)} s`;
}

/**
 * 历史样本统计。分母语义（明确约定，避免“把未知算成失败”）：
 * - uptimeDenominator = healthy + degraded + down（即“有确定观测结果”的样本）。
 *   unknown（无新鲜观测）、untested（从未测试）、maintenance（维护窗口）一律不计入分母。
 * - degraded 视为“仍可用”，但通过 degradePercent 单独区分降级占比。
 * - coverageRatio = observedCount / (total - maintenance)，即剔除维护窗口后的观测覆盖率；
 *   unknown / untested 会拉低覆盖率，而不会拉低可用率。
 * @param {Array<{at?: string, status?: string}>} history
 */
export function getHistoryStats(history) {
  const list = Array.isArray(history) ? history : [];
  const counts = {
    healthy: 0,
    degraded: 0,
    unknown: 0,
    untested: 0,
    maintenance: 0,
    down: 0,
    other: 0,
  };

  for (const entry of list) {
    const status = entry && typeof entry === 'object' ? entry.status : undefined;
    if (Object.prototype.hasOwnProperty.call(counts, status)) {
      counts[status] += 1;
    } else {
      counts.other += 1;
    }
  }

  const total = list.length;
  const expectedCount = Math.max(0, total - counts.maintenance);
  const observedCount = counts.healthy + counts.degraded + counts.down;
  const availableCount = counts.healthy + counts.degraded;

  return {
    total,
    counts,
    expectedCount,
    observedCount,
    uptimeDenominator: observedCount,
    availableCount,
    degradedCount: counts.degraded,
    unknownCount: counts.unknown,
    untestedCount: counts.untested,
    maintenanceCount: counts.maintenance,
    uptimePercent: observedCount > 0 ? round((availableCount / observedCount) * 100, 1) : null,
    degradePercent: observedCount > 0 ? round((counts.degraded / observedCount) * 100, 1) : null,
    coverageRatio: expectedCount > 0 ? round(observedCount / expectedCount, 3) : null,
  };
}

/**
 * 由快照派生展示视图。纯函数：深拷贝后计算，绝不修改传入快照。
 *
 * 判定规则：
 * - 服务 observedAt 距离 now 的年龄 >= collector.staleAfterSeconds 秒 => stale=true，status 置为 'unknown'。
 *   原始上报值保留在 reportedStatus，便于区分“上报值”与“当前可用结论”。
 * - observedAt 缺失或非法同样视为过期（stale），绝不当作“刚观测过”。
 * - untested 从未被测试，即使时间戳变旧也保持 untested，不改写成 unknown。
 * - 过期服务的当前 latencyMs 置为 null，原始值保留在 reportedLatencyMs，
 *   避免把历史延迟当作当前延迟展示。
 * - 采集器心跳同理：lastHeartbeatAt 缺失/非法或年龄 >= staleAfterSeconds*1000 => collector.stale=true。
 * - overall 先判 down，再判“没有任何已评估服务 => unknown”，然后才判 degraded/unknown；
 *   演示环境始终不宣称“全部业务已测试”。
 *
 * @param {object} snapshot createDemoSnapshot 的返回值
 * @param {number} [now] 判定基准时间（毫秒）
 */
export function deriveView(snapshot, now = Date.now()) {
  const clone = structuredClone(snapshot);
  const baseTime = toMillis(clone.sampledAt) ?? now;

  const collectorIn = clone.collector ?? {};
  const staleAfterSeconds =
    Number.isFinite(collectorIn.staleAfterSeconds) && collectorIn.staleAfterSeconds > 0
      ? collectorIn.staleAfterSeconds
      : 90;
  const staleMs = staleAfterSeconds * 1000;

  const services = (Array.isArray(clone.services) ? clone.services : []).map((service) => {
    const reportedStatus = service.status;
    const observedAtMs = toMillis(service.observedAt);
    const observedAgeMs = observedAtMs === null ? null : Math.max(0, now - observedAtMs);
    // 缺失/非法时间戳按“过期”处理，绝不能当作新鲜观测。
    const stale = observedAgeMs === null || observedAgeMs >= staleMs;
    // untested 从未被测过：时间戳变旧也不改写成未知，避免凭空“制造”一次测试结论。
    const status = stale && reportedStatus !== 'untested' ? 'unknown' : reportedStatus;
    return {
      ...service,
      status,
      reportedStatus,
      // 保留原始上报延迟供审计；当前延迟在过期时置空，避免把历史值当作当前值展示。
      reportedLatencyMs: service.latencyMs,
      latencyMs: stale ? null : service.latencyMs,
      stale,
      observedAgeMs,
      observedAgeSeconds: observedAgeMs === null ? null : Math.round(observedAgeMs / 1000),
    };
  });

  const lastHeartbeatMs = toMillis(collectorIn.lastHeartbeatAt);
  const heartbeatAgeMs = lastHeartbeatMs === null ? null : Math.max(0, now - lastHeartbeatMs);
  // 心跳缺失/非法同样视为过期，而不是默认新鲜。
  const heartbeatStale = heartbeatAgeMs === null || heartbeatAgeMs >= staleMs;

  const statusCounts = STATUSES.reduce((acc, status) => {
    acc[status] = 0;
    return acc;
  }, {});
  for (const service of services) {
    if (!Object.prototype.hasOwnProperty.call(statusCounts, service.status)) {
      statusCounts[service.status] = 0;
    }
    statusCounts[service.status] += 1;
  }

  const applicationServices = services.filter((service) => service.group === 'application');
  const businessTested = applicationServices.some(
    (service) => service.status === 'healthy' || service.status === 'degraded' || service.status === 'down',
  );
  const assessedServices = services.filter(
    (service) => !EXCLUDED_FROM_UPTIME.includes(service.status),
  );
  const staleCount = services.filter((service) => service.stale).length;

  let overallStatus;
  if (statusCounts.down > 0) {
    overallStatus = 'down';
  } else if (assessedServices.length === 0) {
    // 没有任何已评估服务时整体只能为未知，必须先于 degraded/unknown 分支判断。
    overallStatus = 'unknown';
  } else if (statusCounts.degraded > 0 || statusCounts.unknown > 0) {
    overallStatus = 'degraded';
  } else {
    overallStatus = 'healthy';
  }

  return {
    ...clone,
    collector: {
      ...collectorIn,
      lastHeartbeatAt: collectorIn.lastHeartbeatAt ?? null,
      heartbeatAgeMs,
      heartbeatAgeSeconds: heartbeatAgeMs === null ? null : Math.round(heartbeatAgeMs / 1000),
      stale: heartbeatStale,
    },
    services,
    statusCounts,
    overall: {
      status: overallStatus,
      businessTested,
      // 演示快照没有外部业务探针，始终不宣称“所有业务已验证可用”。
      claimsAllBusinessTested: false,
      allServicesAssessed: statusCounts.untested === 0 && statusCounts.unknown === 0,
      staleCount,
      unknownCount: statusCounts.unknown,
      untestedCount: statusCounts.untested,
      note: businessTested
        ? '整体状态不表示所有回家业务、链路与路由均已验证。'
        : '家庭业务尚未测试；整体状态不代表所有回家业务可用。',
    },
    generatedAt: new Date(baseTime).toISOString(),
    derivedAt: new Date(now).toISOString(),
  };
}

'use strict';

const $ = id => document.getElementById(id);
let machines = [];
let selectedID = null;
let clockOffset = 0;
let connected = false;

function node(tag, text, className) {
  const element = document.createElement(tag);
  if (text !== undefined && text !== null) element.textContent = text;
  if (className) element.className = className;
  return element;
}
function bytes(value, digits = 1) {
  if (!Number.isFinite(value)) return 'Unavailable';
  if (value < 1024) return `${value} B`;
  const units = ['KiB', 'MiB', 'GiB', 'TiB'];
  let n = value / 1024, i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return `${Number(n.toFixed(digits))} ${units[i]}`;
}
function age(when) {
  const seconds = Math.max(0, (Date.now() + clockOffset - new Date(when)) / 1000);
  if (seconds < 5) return 'just now';
  if (seconds < 60) return `${Math.floor(seconds)}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}
function online(machine) { return Date.now() + clockOffset - new Date(machine.received_at) < 90000; }
function dot(isOnline) { return node('span', null, `dot${isOnline ? '' : ' offline'}`); }
function stateLabel(section) {
  const names = {unsupported: 'Not collected', permission_denied: 'Requires root', error: 'Unavailable'};
  const label = node('span', names[section?.status] || 'Unavailable', 'field-state');
  label.title = section?.error || 'No observation is available.';
  return label;
}
function data(section) { return section?.status === 'ok' && section.data !== undefined ? section.data : null; }
function field(list, label, value, mono = false) {
  const row = node('div');
  const dt = node('dt', label), dd = node('dd', null, mono ? 'mono' : '');
  if (value instanceof Node) dd.append(value); else dd.textContent = value || 'Unavailable';
  row.append(dt, dd); list.append(row);
}
function clearFields(id, rows) {
  const list = $(id); list.replaceChildren();
  for (const row of rows) field(list, ...row);
}
function boolValue(section) { return data(section) === null ? stateLabel(section) : (section.data ? 'On' : 'Off'); }
function displayTime(value) { return new Date(value).toLocaleString(undefined, {dateStyle: 'medium', timeStyle: 'short'}); }
function uptime(seconds) {
  const days = Math.floor(seconds / 86400), hours = Math.floor(seconds % 86400 / 3600), minutes = Math.floor(seconds % 3600 / 60);
  return days ? `${days}d ${hours}h` : `${hours}h ${minutes}m`;
}
function primaryAddress(snapshot) {
  for (const iface of data(snapshot.network) || []) {
    if (!iface.up || iface.loopback) continue;
    const address = iface.addresses.find(value => /^\d+\./.test(value));
    if (address) return address.split('/')[0];
  }
  return 'No IPv4 address';
}
function matchingMachines() {
  const query = $('search').value.toLowerCase().trim();
  return machines.filter(({snapshot: s}) => {
    const system = data(s.system), hw = data(s.hardware);
    return [system?.hostname, system?.os_version, hw?.model_name, hw?.chip, ...(data(s.network) || []).flatMap(i => i.addresses)].join(' ').toLowerCase().includes(query);
  });
}
function coreConfiguration(hardware) {
  if (!hardware) return 'Core configuration unavailable';
  const groups = (hardware.core_groups || []).filter(group => Number.isInteger(group.physical_cores) && group.physical_cores > 0);
  const labels = new Map([['efficiency', 'E'], ['performance', 'P'], ['super', 'S']]);
  const parts = [];
  for (const [name, label] of labels) {
    const count = groups.filter(group => group.name.toLowerCase() === name).reduce((sum, group) => sum + group.physical_cores, 0);
    if (count) parts.push(`${count}${label}`);
  }
  for (const group of groups) {
    if (!labels.has(group.name.toLowerCase())) parts.push(`${group.physical_cores} ${group.name}`);
  }
  if (parts.length) return parts.join(' / ');
  if (!hardware.physical_cores) return 'Core configuration unavailable';
  return hardware.logical_cores > hardware.physical_cores ? `${hardware.physical_cores}C / ${hardware.logical_cores}T` : `${hardware.physical_cores} cores`;
}
function cpuUsage(section) {
  const cpu = data(section);
  if (!cpu) return stateLabel(section);
  if (!Number.isFinite(cpu.usage_percent) || cpu.usage_percent < 0 || !Number.isInteger(cpu.logical_cores) || cpu.logical_cores <= 0 || cpu.usage_percent > cpu.logical_cores * 100) {
    return node('span', 'Unavailable', 'field-state');
  }
  const value = node('span', `${cpu.usage_percent.toFixed(1)}%`, 'cpu-usage');
  value.title = `100% = one logical core. Total capacity: ${cpu.logical_cores * 100}%.`;
  return value;
}
function cpuCell(snapshot, hardware) {
  const cell = node('td'), cpu = data(snapshot.cpu), value = cpuUsage(snapshot.cpu);
  const summary = node('div', null, 'usage-summary cpu-summary'), meter = node('div', null, 'cpu-meter');
  if (value.className === 'cpu-usage') {
    const bar = node('div', null, 'bar');
    bar.append(barPiece(cpu.usage_percent / (cpu.logical_cores * 100), 'active'));
    bar.setAttribute('role', 'progressbar');
    bar.setAttribute('aria-label', 'CPU usage');
    bar.setAttribute('aria-valuemin', '0');
    bar.setAttribute('aria-valuemax', String(cpu.logical_cores * 100));
    bar.setAttribute('aria-valuenow', String(cpu.usage_percent));
    bar.setAttribute('aria-valuetext', `${cpu.usage_percent.toFixed(1)}% of ${cpu.logical_cores * 100}% total capacity`);
    bar.title = value.title;
    meter.append(bar);
  }
  meter.append(value);
  const configuration = node('span', coreConfiguration(hardware), 'usage-values');
  configuration.title = (hardware?.core_groups || []).map(group => `${group.physical_cores} ${group.name}`).join(' · ');
  const description = node('span', null, 'cpu-description');
  description.append(node('span', hardware?.chip || 'Chip unavailable', 'cpu-model'), document.createTextNode(' · '), configuration);
  summary.append(meter, description);
  cell.append(summary);
  return cell;
}
function transferBytes(value) {
  if (!Number.isFinite(value) || value < 0) return 'Unavailable';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let index = 0;
  while (value >= 1000 && index < units.length - 1) { value /= 1000; index++; }
  return `${index === 0 ? Math.round(value) : value.toFixed(1)} ${units[index]}`;
}
function recentTraffic(machine) {
  return connected && Date.now() + clockOffset - new Date(machine.snapshot.traffic?.observed_at) < 10000;
}
function trafficPair(received, sent, suffix, className) {
  const row = node('div', null, className);
  for (const [arrow, name, value] of [['↓', 'Download', received], ['↑', 'Upload', sent]]) {
    const text = `${transferBytes(value)}${suffix}`;
    const part = node('span', `${arrow} ${text}`);
    part.setAttribute('aria-label', `${name}: ${text}`);
    row.append(part);
  }
  return row;
}
function networkCell(machine) {
  const cell = node('td'), traffic = data(machine.snapshot.traffic);
  if (!traffic) { cell.append(stateLabel(machine.snapshot.traffic)); return cell; }
  const summary = node('div', null, 'network-summary');
  if (traffic.rate && recentTraffic(machine)) {
    summary.append(trafficPair(traffic.rate.receive_bytes_per_second, traffic.rate.send_bytes_per_second, '/s', 'network-rate'));
    summary.title = `Average over the last ${traffic.rate.window_seconds.toFixed(1)} seconds. `;
  } else {
    summary.append(node('span', recentTraffic(machine) ? 'Measuring speed…' : 'No recent sample', 'field-state'));
  }
  summary.append(trafficPair(traffic.received_bytes, traffic.sent_bytes, '', 'network-total'));
  summary.title += 'Download / upload totals since boot on Ethernet and Wi-Fi. Interface resets can reset these counters.';
  cell.append(summary);
  return cell;
}
function renderList() {
  const filtered = matchingMachines();
  const focusedDevice = document.activeElement?.dataset.deviceId;
  const rows = $('machine-rows'); rows.replaceChildren();
  for (const machine of filtered) {
    const s = machine.snapshot, hw = data(s.hardware), system = data(s.system);
    const tr = node('tr', null, s.device_id === selectedID ? 'selected' : '');
    const first = node('td'), button = node('button', null, 'machine-button');
    button.type = 'button';
    button.setAttribute('aria-haspopup', 'dialog');
    button.setAttribute('aria-controls', 'detail');
    button.setAttribute('aria-expanded', String(s.device_id === selectedID));
    button.dataset.deviceId = s.device_id;
    const isOnline = online(machine), presence = dot(isOnline);
    presence.classList.add('machine-presence');
    presence.title = isOnline ? `Online · Last report ${age(machine.received_at)}` : `No recent report · Last seen ${age(machine.received_at)}`;
    presence.setAttribute('role', 'img');
    presence.setAttribute('aria-label', presence.title);
    const version = system?.os_version ? `macOS ${system.os_version}${system.os_extra ? ` ${system.os_extra}` : ''}` : 'macOS unavailable';
    const name = node('span');
    name.append(
      node('span', system?.hostname || 'Unknown machine', 'machine-title'),
      node('span', `${hw?.model_name || 'Mac'} · ${version}`, 'machine-meta'),
      node('span', primaryAddress(s), 'machine-address'),
    );
    button.append(presence, name);
    button.addEventListener('click', () => openDetail(s.device_id));
    first.append(button);
    tr.append(first, cpuCell(s, hw), memoryCell(s, hw), storageCell(s.storage), networkCell(machine));
    rows.append(tr);
    if (focusedDevice === s.device_id) button.focus({preventScroll: true});
  }
  $('machine-count').textContent = machines.length;
  $('empty-state').hidden = machines.length !== 0;
  $('no-results').hidden = machines.length === 0 || filtered.length !== 0;
  const available = machines.filter(online).length;
  $('fleet-status').replaceChildren(dot(connected && available > 0), document.createTextNode(connected ? `${available} online · ${machines.length} total` : 'Receiver unavailable'));
  const newest = machines.reduce((latest, m) => Math.max(latest, new Date(m.received_at).getTime()), 0);
  $('updated').textContent = newest ? `Live · Last report ${age(newest)}` : 'Waiting for the first report';
}
function barPiece(fraction, className) {
  const piece = node('div', null, `bar-piece ${className}`);
  piece.style.width = `${Math.max(0, Math.min(1, fraction)) * 100}%`;
  return piece;
}
function memorySegments(memory) {
  return [['Active', memory.active_bytes, 'active'], ['Wired', memory.wired_bytes, 'wired'], ['Compressed', memory.compressed_bytes, 'compressed']];
}
function usageSummary(used, free, bar, explanation) {
  const gigabytes = value => `${(value / 1e9).toFixed(1)} GB`;
  const summary = node('div', null, 'usage-summary');
  const description = `${gigabytes(used)} used / ${gigabytes(free)} free`;
  summary.title = `${description}. ${explanation}`;
  bar.setAttribute('aria-hidden', 'true');
  const values = node('span', null, 'usage-values');
  values.setAttribute('aria-label', description);
  values.append(node('span', gigabytes(used)), document.createTextNode(` / ${gigabytes(free)}`));
  summary.append(bar, values);
  return summary;
}
function memoryCell(snapshot, hardware) {
  const cell = node('td'), memory = data(snapshot.memory), total = hardware?.memory_bytes;
  if (!memory || !hardware) {
    cell.append(stateLabel(!memory ? snapshot.memory : snapshot.hardware));
    return cell;
  }
  const segments = memorySegments(memory);
  if (!Number.isFinite(total) || total <= 0 || ![memory.free_bytes, ...segments.map(([, value]) => value)].every(value => Number.isFinite(value) && value >= 0)) {
    cell.append(node('span', 'Unavailable', 'field-state'));
    return cell;
  }
  const free = Math.min(total, memory.free_bytes), used = total - free;
  const bar = node('div', null, 'bar');
  let remaining = used;
  for (const [, value, color] of segments) {
    const amount = Math.min(remaining, value);
    bar.append(barPiece(amount / total, color));
    remaining -= amount;
  }
  bar.append(barPiece(remaining / total, 'other-used'));
  const summary = usageSummary(used, free, bar, 'Used includes cached and inactive memory; free is the reported free pages.');
  if (memory.pressure && memory.pressure !== 'unknown') summary.title += ` Memory pressure: ${memory.pressure}.`;
  cell.append(summary);
  return cell;
}
function storageCell(section) {
  const cell = node('td'), storage = data(section);
  if (!storage) {
    cell.append(stateLabel(section));
    return cell;
  }
  const total = storage.total_bytes;
  if (!Number.isFinite(total) || total <= 0 || !Number.isFinite(storage.free_bytes) || storage.free_bytes < 0) {
    cell.append(node('span', 'Unavailable', 'field-state'));
    return cell;
  }
  const free = Math.min(total, storage.free_bytes), used = total - free;
  const bar = node('div', null, 'bar');
  bar.append(barPiece(used / total, 'used'));
  const summary = usageSummary(used, free, bar, 'Startup data volume; free is the filesystem-reported free space.');
  if (Number.isFinite(storage.available_bytes) && storage.available_bytes / total < .1) {
    summary.classList.add(storage.available_bytes / total < .05 ? 'storage-critical' : 'storage-warning');
    summary.title += ` Low storage: ${transferBytes(storage.available_bytes)} available.`;
  }
  cell.append(summary);
  return cell;
}
function renderMemory(s, hw) {
  const box = $('memory-content'), m = data(s.memory); box.replaceChildren();
  $('memory-pressure').textContent = m && m.pressure !== 'unknown' ? `${m.pressure[0].toUpperCase()}${m.pressure.slice(1)} pressure` : 'Pressure unavailable';
  $('memory-pressure').className = `small-status ${m?.pressure || ''}`;
  if (!m || !hw) { box.append(stateLabel(!m ? s.memory : s.hardware)); return; }
  const bar = node('div', null, 'bar'), legend = node('div', null, 'legend');
  let remaining = 1;
  for (const [label, value, color] of memorySegments(m)) {
    const fraction = Math.min(remaining, value / hw.memory_bytes); remaining -= fraction;
    bar.append(barPiece(fraction, color));
    const item = node('span', null, 'legend-item'); item.append(node('span', null, `swatch ${color}`), document.createTextNode(`${label} ${bytes(value)}`)); legend.append(item);
  }
  const other = node('span', null, 'legend-item'); other.append(node('span', null, 'swatch'), document.createTextNode('Other / free')); legend.append(other);
  const fields = node('dl', null, 'fields resource-fields');
  field(fields, 'Installed', bytes(hw.memory_bytes));
  field(fields, 'Free pages', bytes(m.free_bytes));
  field(fields, 'Swap used', `${bytes(m.swap_used_bytes)} / ${bytes(m.swap_total_bytes)}`);
  box.append(bar, legend, fields);
}
function renderStorage(s) {
  const box = $('storage-content'), disk = data(s.storage); box.replaceChildren();
  if (!disk) { box.append(stateLabel(s.storage)); return; }
  const used = Math.max(0, disk.total_bytes - disk.free_bytes);
  const bar = node('div', null, 'bar'); bar.append(barPiece(used / disk.total_bytes, 'used'));
  const fields = node('dl', null, 'fields resource-fields');
  field(fields, 'Capacity', bytes(disk.total_bytes));
  field(fields, 'Used', bytes(used));
  field(fields, 'Available', bytes(disk.available_bytes));
  box.append(bar, fields, node('p', disk.path, 'metric-note'));
}
function renderNetwork(s) {
  const box = $('network-content'); box.replaceChildren();
  const interfaces = data(s.network);
  if (!interfaces) { box.append(stateLabel(s.network)); return; }
  const visible = interfaces.filter(i => $('show-all-interfaces').checked || (i.up && !i.loopback && i.addresses.length > 0 && i.mac));
  if (!visible.length) { box.append(node('p', 'No active interfaces with a hardware address. Select “Show all interfaces” to inspect the rest.', 'unavailable')); return; }
  for (const iface of visible) {
    const item = node('section', null, 'network-interface'), heading = node('div', null, 'interface-heading');
    const state = node('span', iface.up ? 'Up' : 'Down', 'muted');
    state.title = 'Interface administrative state';
    heading.append(node('h4', iface.name), state);
    const fields = node('dl', null, 'fields'), addresses = node('span');
    for (const address of iface.addresses) addresses.append(node('span', address, 'address'));
    if (!iface.addresses.length) addresses.textContent = 'No address';
    field(fields, 'IP address', addresses, true);
    field(fields, 'MAC address', iface.mac || 'Not applicable', true);
    item.append(heading, fields);
    box.append(item);
  }
}
function renderTraffic(machine) {
  const box = $('traffic-content'), traffic = data(machine.snapshot.traffic);
  box.replaceChildren();
  if (!traffic) { box.append(stateLabel(machine.snapshot.traffic)); return; }
  const fields = node('dl', null, 'fields');
  const rate = recentTraffic(machine) ? traffic.rate : null;
  field(fields, 'Download', rate ? `${transferBytes(rate.receive_bytes_per_second)}/s` : 'Awaiting sample');
  field(fields, 'Upload', rate ? `${transferBytes(rate.send_bytes_per_second)}/s` : 'Awaiting sample');
  field(fields, 'Downloaded', transferBytes(traffic.received_bytes));
  field(fields, 'Uploaded', transferBytes(traffic.sent_bytes));
  if (rate) field(fields, 'Sample window', `${rate.window_seconds.toFixed(2)} s`);
  box.append(fields, node('p', 'Ethernet and Wi-Fi totals since boot. An interface reset can reset its counters.', 'section-description'));
  box.append(node('p', `Included: ${traffic.interfaces.join(', ')}`, 'section-description'));
}
function renderDetail() {
  const machine = machines.find(m => m.snapshot.device_id === selectedID);
  if (!machine) { if ($('detail').open) $('detail').close(); return; }
  const s = machine.snapshot, hw = data(s.hardware), system = data(s.system);
  $('detail-name').textContent = system?.hostname || 'Unknown machine';
  $('detail-subtitle').textContent = [hw?.model_name, hw?.chip, hw?.memory_bytes ? bytes(hw.memory_bytes) : null].filter(Boolean).join(' · ');
  const isOnline = online(machine);
  $('detail-status').className = `status-pill${isOnline ? '' : ' stale'}`;
  $('detail-status').replaceChildren(dot(isOnline), document.createTextNode(isOnline ? 'Online' : `Last seen ${age(machine.received_at)}`));
  $('detail-last-report').textContent = isOnline ? `Reported ${age(machine.received_at)}` : 'Showing the last report';
  $('detail-connection-error').hidden = connected;
  renderMemory(s, hw); renderStorage(s);
  clearFields('system-fields', system ? [
    ['macOS', `${system.os_version}${system.os_extra ? ` ${system.os_extra}` : ''} (${system.os_build})`],
    ['Uptime', uptime(system.uptime_seconds)], ['CPU usage', cpuUsage(s.cpu)], ['Load average', system.load_average.map(v => v.toFixed(2)).join(' / ') + '  (1 / 5 / 15 min)'],
    ['Console user', system.console_user || 'No active user'],
  ] : [['System status', stateLabel(s.system)]]);
  clearFields('security-fields', [
    ['Remote Login', boolValue(s.security.remote_login)], ['FileVault', boolValue(s.security.filevault)],
    ['Automatic login', data(s.security.auto_login) === null ? stateLabel(s.security.auto_login) : (s.security.auto_login.data || 'Off')],
    ['Activation Lock at startup', hw?.activation_lock === 'enabled' ? 'On' : hw?.activation_lock === 'disabled' ? 'Off' : 'Unavailable'],
  ]);
  renderNetwork(s); renderTraffic(machine);
  $('device-model').textContent = hw?.model_id || '';
  $('inventory-time').textContent = s.hardware?.observed_at ? `Inventory from ${displayTime(s.hardware.observed_at)}` : 'Inventory unavailable';
  clearFields('hardware-fields', hw ? [
    ['Model', hw.model_name], ['Model number', hw.model_number, true], ['Chip', hw.chip],
    ['CPU cores', `${hw.physical_cores} physical · ${hw.logical_cores} logical`],
    ['Core groups', hw.core_groups?.length ? hw.core_groups.map(g => `${g.physical_cores} ${g.name}`).join(' · ') : 'Not reported'],
  ] : [['Hardware status', stateLabel(s.hardware)]]);
  clearFields('firmware-fields', hw ? [['Architecture', hw.architecture, true], ['System firmware', hw.firmware, true], ['OS loader', hw.os_loader, true], ['Installed memory', bytes(hw.memory_bytes)]] : []);
  clearFields('identifier-fields', hw ? [['Serial number', hw.serial_number, true], ['Hardware UUID', hw.hardware_uuid, true], ['Provisioning UDID', hw.provisioning_id, true], ['Hako device ID', s.device_id, true]] : []);
  $('agent-info').textContent = `Agent ${s.agent.version} · ${s.agent.uid === 0 ? 'Collected as root' : `Collected as user ${s.agent.uid}`} · Reported ${age(machine.received_at)}`;
}
function openDetail(id) {
  if (!machines.some(machine => machine.snapshot.device_id === id)) return;
  selectedID = id;
  render();
  document.body.classList.add('drawer-open');
  $('detail').showModal();
  selectDetailTab('overview');
  $('close-detail').focus({preventScroll: true});
}
function selectDetailTab(id) {
  document.querySelectorAll('.drawer-section').forEach(section => { section.hidden = section.id !== id; });
  document.querySelectorAll('.detail-tabs button').forEach(button => {
    const active = button.dataset.section === id;
    button.setAttribute('aria-selected', String(active));
    button.tabIndex = active ? 0 : -1;
  });
  $('detail-scroll').scrollTop = 0;
}
function render() { renderList(); renderDetail(); }
function receiveFleet(result) {
  machines = result.machines;
  clockOffset = new Date(result.server_time).getTime() - Date.now();
  connected = true; $('connection-error').hidden = true;
  render();
}
function disconnected() {
  connected = false;
  $('connection-error').textContent = 'Connection interrupted. Reconnecting automatically; showing the last received data.';
  $('connection-error').hidden = false;
  render();
}
$('search').addEventListener('input', render);
$('show-all-interfaces').addEventListener('change', renderDetail);
$('close-detail').addEventListener('click', () => $('detail').close());
$('detail').addEventListener('close', () => {
  const previousID = selectedID;
  selectedID = null;
  document.body.classList.remove('drawer-open');
  renderList();
  const trigger = [...document.querySelectorAll('.machine-button')].find(button => button.dataset.deviceId === previousID);
  (trigger || $('search')).focus({preventScroll: true});
});
$('detail').addEventListener('click', event => {
  if (event.target !== $('detail')) return;
  const bounds = $('detail').getBoundingClientRect();
  if (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom) $('detail').close();
});
const detailTabs = [...document.querySelectorAll('.detail-tabs button')];
detailTabs.forEach((button, index) => {
  button.addEventListener('click', () => selectDetailTab(button.dataset.section));
  button.addEventListener('keydown', event => {
    const destinations = {ArrowRight: (index + 1) % detailTabs.length, ArrowLeft: (index + detailTabs.length - 1) % detailTabs.length, Home: 0, End: detailTabs.length - 1};
    if (!(event.key in destinations)) return;
    event.preventDefault();
    const next = detailTabs[destinations[event.key]];
    selectDetailTab(next.dataset.section);
    next.focus();
  });
});
let events = null;
let checkingSession = false;
function showSignIn() {
  events?.close(); events = null;
  if ($('detail').open) $('detail').close();
  if ($('enrollment').open) $('enrollment').close();
  machines = []; selectedID = null; connected = false;
  $('machine-rows').replaceChildren();
  $('workspace').hidden = true; $('sign-out').hidden = true; $('sign-in').hidden = false;
}
function startEvents() {
  $('sign-in').hidden = true; $('workspace').hidden = false; $('sign-out').hidden = false;
  events?.close();
  events = new EventSource('/api/events');
  events.addEventListener('machines', event => {
    try { receiveFleet(JSON.parse(event.data)); } catch { disconnected(); }
  });
  events.addEventListener('error', async () => {
    disconnected();
    if (checkingSession) return;
    checkingSession = true;
    try { if ((await fetch('/api/session')).status === 401) showSignIn(); }
    catch { /* The stream reconnects when the receiver returns. */ }
    finally { checkingSession = false; }
  });
}
async function api(path, method = 'GET', body) {
  const response = await fetch(path, {method, headers: {'Content-Type': 'application/json'}, body: body === undefined ? undefined : JSON.stringify(body)});
  if (response.status === 401) { showSignIn(); throw new Error('Your session expired. Sign in again.'); }
  if (!response.ok) throw new Error((await response.text()).trim() || `Request failed (${response.status}).`);
  return response.status === 204 ? null : response.json();
}
function showFormError(id, error) { $(id).textContent = error.message; $(id).hidden = false; }
$('sign-in-form').addEventListener('submit', async event => {
  event.preventDefault();
  const button = event.submitter;
  button.disabled = true; $('sign-in-error').hidden = true;
  try {
    const response = await fetch('/api/session', {method: 'POST', headers: {Authorization: `Bearer ${$('admin-key').value.trim()}`}});
    if (!response.ok) throw new Error(response.status === 401 ? 'The administrator key is not valid.' : 'Unable to sign in. Try again.');
    $('admin-key').value = ''; startEvents();
  } catch (error) { showFormError('sign-in-error', error); }
  finally { button.disabled = false; }
});
$('sign-out').addEventListener('click', async () => {
  try { await api('/api/logout', 'POST'); showSignIn(); }
  catch (error) { $('connection-error').textContent = error.message; $('connection-error').hidden = false; }
});
async function pendingEnrollments() {
  const items = await api('/api/enrollments');
  const list = $('pending-enrollments'); list.replaceChildren();
  const pending = items.filter(item => !item.device_id && !item.revoked && new Date(item.expires_at) > Date.now()).sort((a,b) => b.created_at.localeCompare(a.created_at));
  for (const item of pending) {
    const row = node('div', null, 'pending-key'), name = node('span', item.label || 'Unnamed key');
    name.append(node('small', `Expires ${displayTime(item.expires_at)}`));
    const revoke = node('button', 'Revoke', 'text-button danger'); revoke.type = 'button';
    revoke.addEventListener('click', async () => {
      revoke.disabled = true;
      try { await api(`/api/enrollments/${encodeURIComponent(item.id)}`, 'DELETE'); await pendingEnrollments(); }
      catch (error) { showFormError('enrollment-error', error); revoke.disabled = false; }
    });
    row.append(name, revoke); list.append(row);
  }
  if (!pending.length) list.append(node('p', 'No pending keys.'));
}
$('add-machine').addEventListener('click', async () => {
  $('enrollment-error').hidden = true;
  $('enrollment').showModal();
  try { await pendingEnrollments(); } catch (error) { showFormError('enrollment-error', error); }
});
$('close-enrollment').addEventListener('click', () => $('enrollment').close());
$('enrollment').addEventListener('close', () => {
  $('enrollment-key').value = ''; $('enrollment-result').hidden = true; $('enrollment-form').hidden = false;
  $('enrollment-form').reset(); $('copy-enrollment-key').textContent = 'Copy key';
});
$('enrollment-form').addEventListener('submit', async event => {
  event.preventDefault();
  const button = event.submitter; button.disabled = true; $('enrollment-error').hidden = true;
  try {
    const result = await api('/api/enrollments', 'POST', {label: $('enrollment-label').value, ttl_seconds: Number($('enrollment-ttl').value)});
    $('enrollment-key').value = result.key; $('enrollment-form').hidden = true; $('enrollment-result').hidden = false;
    $('enrollment-expiry').textContent = `Expires ${displayTime(result.expires_at)}`;
    await pendingEnrollments();
  } catch (error) { showFormError('enrollment-error', error); }
  finally { button.disabled = false; }
});
$('copy-enrollment-key').addEventListener('click', async () => {
  try { await navigator.clipboard.writeText($('enrollment-key').value); $('copy-enrollment-key').textContent = 'Copied'; }
  catch { $('enrollment-key').select(); $('copy-enrollment-key').textContent = 'Selected for copying'; }
});
$('remove-machine').addEventListener('click', async () => {
  const machine = machines.find(item => item.snapshot.device_id === selectedID);
  if (!machine || !window.confirm(`Remove ${data(machine.snapshot.system)?.hostname || 'this machine'}? Its credentials will be revoked. A new enrollment key is required to join again.`)) return;
  $('remove-machine').disabled = true; $('machine-action-error').hidden = true;
  try { await api(`/api/machines/${encodeURIComponent(selectedID)}`, 'DELETE'); if ($('detail').open) $('detail').close(); }
  catch (error) { showFormError('machine-action-error', error); }
  finally { $('remove-machine').disabled = false; }
});
async function initialize() {
  try { if ((await fetch('/api/session')).ok) startEvents(); else showSignIn(); }
  catch { showSignIn(); showFormError('sign-in-error', new Error('The server is unavailable. Try signing in when it returns.')); }
}
window.setInterval(() => { if (machines.length) render(); }, 2000);
initialize();

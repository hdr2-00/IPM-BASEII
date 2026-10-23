// Dependency-free UI helpers. All data loading is htmx attributes in the
// templates; this file only covers what htmx does not: drag-and-drop, the
// theme control, and active-state bookkeeping.

/* ------------------------------------------------------------- theme --- */
// Three states, not two: "system" follows prefers-color-scheme, and the
// other two override it. A two-state toggle can never get back to system
// once it has been touched.
function currentThemeChoice() {
  try {
    var t = localStorage.getItem('mciipm-theme');
    return (t === 'light' || t === 'dark') ? t : 'system';
  } catch (e) {
    return 'system';
  }
}

function applyTheme(choice) {
  if (choice === 'system') {
    document.documentElement.removeAttribute('data-theme');
    try { localStorage.removeItem('mciipm-theme'); } catch (e) { /* storage blocked */ }
  } else {
    document.documentElement.setAttribute('data-theme', choice);
    try { localStorage.setItem('mciipm-theme', choice); } catch (e) { /* storage blocked */ }
  }
  syncThemeControl();
}

function syncThemeControl() {
  var active = currentThemeChoice();
  document.querySelectorAll('.theme-control [data-theme-choice]').forEach(function (btn) {
    btn.setAttribute('aria-pressed', String(btn.dataset.themeChoice === active));
  });
}

function initThemeControl() {
  document.querySelectorAll('.theme-control [data-theme-choice]').forEach(function (btn) {
    if (btn.dataset.bound) return;
    btn.dataset.bound = '1';
    btn.addEventListener('click', function () { applyTheme(btn.dataset.themeChoice); });
  });
  syncThemeControl();
}

/* ---------------------------------------------------------- dropzone --- */
function initDropzone() {
  const zone = document.getElementById('dropzone');
  const input = document.getElementById('file-input');
  const label = document.getElementById('dropzone-filename');
  if (!zone || !input || zone.dataset.bound) return;
  zone.dataset.bound = '1';

  // The zone is a <label for="file-input">, so click and keyboard
  // activation reach the input natively - only dragging needs JS.
  ['dragenter', 'dragover'].forEach(evt => {
    zone.addEventListener(evt, (e) => {
      e.preventDefault();
      zone.classList.add('dragover');
    });
  });
  ['dragleave', 'drop'].forEach(evt => {
    zone.addEventListener(evt, (e) => {
      e.preventDefault();
      zone.classList.remove('dragover');
    });
  });
  zone.addEventListener('drop', (e) => {
    const files = e.dataTransfer.files;
    if (files && files.length > 0) {
      input.files = files;
      updateFilename();
    }
  });
  input.addEventListener('change', updateFilename);

  function updateFilename() {
    if (input.files && input.files.length > 0) {
      label.textContent = input.files[0].name;
      label.style.display = 'block';
      zone.classList.add('has-file');
    }
  }
}

/* ------------------------------------------------------ active state --- */
function selectRecordRow(row) {
  document.querySelectorAll('#records-tbody tr').forEach(r => r.classList.remove('selected'));
  row.classList.add('selected');
}

function setActiveTab(btn) {
  document.querySelectorAll('.tab-btn').forEach(b => {
    b.classList.remove('active');
    b.setAttribute('aria-selected', 'false');
  });
  btn.classList.add('active');
  btn.setAttribute('aria-selected', 'true');
}

// Jumping from a finding or a file-strip segment to a record also moves
// the tab strip's state, so Records does not look inactive while showing
// its own content.
function activateRecordsTab() {
  const records = document.querySelectorAll('.tab-btn')[1];
  if (records) setActiveTab(records);
}

function setActiveFilter(btn, groupSelector) {
  document.querySelectorAll(groupSelector + ' .filter-chip').forEach(b => b.classList.remove('active'));
  btn.classList.add('active');
}

function init() {
  initDropzone();
  initThemeControl();
}

document.addEventListener('DOMContentLoaded', init);
// Re-run after htmx swaps in content that may contain these controls.
document.body.addEventListener('htmx:afterSwap', init);

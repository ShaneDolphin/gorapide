// Rapide Studio - Visual Architecture Editor
// Plain JavaScript frontend for rapide-studio server.

// --- Cytoscape initialization ---

const cy = cytoscape({
    container: document.getElementById('cy'),
    style: [
        {
            selector: 'node',
            style: {
                'label': 'data(label)',
                'background-color': '#89b4fa',
                'color': '#cdd6f4',
                'text-valign': 'center',
                'text-halign': 'center',
                'font-size': '12px',
                'width': 120,
                'height': 50,
                'shape': 'roundrectangle',
                'text-wrap': 'wrap',
                'text-max-width': 110
            }
        },
        {
            selector: 'edge',
            style: {
                'label': 'data(label)',
                'width': 2,
                'line-color': '#585b70',
                'target-arrow-color': '#585b70',
                'target-arrow-shape': 'triangle',
                'curve-style': 'bezier',
                'font-size': '10px',
                'color': '#a6adc8'
            }
        },
        {
            selector: 'edge[kind="pipe"]',
            style: {
                'line-color': '#a6e3a1',
                'target-arrow-color': '#a6e3a1'
            }
        },
        {
            selector: 'edge[kind="agent"]',
            style: {
                'line-style': 'dotted',
                'line-color': '#f9e2af',
                'target-arrow-color': '#f9e2af'
            }
        },
        {
            selector: ':selected',
            style: {
                'border-color': '#f38ba8',
                'border-width': 3
            }
        }
    ],
    layout: { name: 'grid' }
});

// Track the current architecture ID (set after save or load).
let currentArchID = null;

// --- WebSocket handling ---

let ws = null;

function connectWS() {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    ws = new WebSocket(proto + '//' + location.host + '/ws');

    ws.onopen = function() {
        console.log('ws: connected');
    };

    ws.onmessage = function(evt) {
        var msg;
        try {
            msg = JSON.parse(evt.data);
        } catch (e) {
            console.error('ws: bad message', e);
            return;
        }

        if (msg.type === 'event') {
            var e;
            // msg.data may already be an object or a JSON string.
            if (typeof msg.data === 'string') {
                try { e = JSON.parse(msg.data); } catch (_) { e = msg.data; }
            } else {
                e = msg.data;
            }
            addEventToFeed(e);
        } else if (msg.type === 'sim_started') {
            document.getElementById('sim-status').textContent = 'Simulation running';
            document.getElementById('btn-start').disabled = true;
            document.getElementById('btn-stop').disabled = false;
        } else if (msg.type === 'sim_stopped') {
            document.getElementById('sim-status').textContent = '';
            document.getElementById('btn-start').disabled = false;
            document.getElementById('btn-stop').disabled = true;
        }
    };

    ws.onclose = function() {
        console.log('ws: disconnected');
        ws = null;
    };

    ws.onerror = function(err) {
        console.error('ws: error', err);
    };
}

// Connect the WebSocket immediately on page load.
connectWS();

// --- Event feed ---

var eventCount = 0;

function addEventToFeed(e) {
    eventCount++;
    document.getElementById('event-count').textContent = '(' + eventCount + ')';
    var list = document.getElementById('event-list');
    var item = document.createElement('div');
    item.className = 'event-item';
    var name = e.Name || e.name || '?';
    var source = e.Source || e.source || '?';
    var params = e.Params || e.params;
    var paramsStr = params ? JSON.stringify(params) : '';
    item.innerHTML =
        '<span class="event-name">' + escapeHTML(name) + '</span> ' +
        '<span class="event-source">@' + escapeHTML(source) + '</span> ' +
        '<span class="event-params">' + escapeHTML(paramsStr) + '</span>';
    list.prepend(item);
}

function escapeHTML(str) {
    if (!str) return '';
    return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

// --- Inspector (click handlers) ---

cy.on('tap', 'node', function(evt) {
    var node = evt.target;
    var content = document.getElementById('inspector-content');
    content.innerHTML =
        '<div class="inspector-field"><label>ID</label><input value="' + escapeHTML(node.data('id')) + '" readonly></div>' +
        '<div class="inspector-field"><label>Label</label><input value="' + escapeHTML(node.data('label') || '') + '" readonly></div>' +
        '<div class="inspector-field"><label>Interface</label><input value="' + escapeHTML(node.data('iface') || '') + '" readonly></div>';
});

cy.on('tap', 'edge', function(evt) {
    var edge = evt.target;
    var content = document.getElementById('inspector-content');
    content.innerHTML =
        '<div class="inspector-field"><label>From</label><input value="' + escapeHTML(edge.data('source')) + '" readonly></div>' +
        '<div class="inspector-field"><label>To</label><input value="' + escapeHTML(edge.data('target')) + '" readonly></div>' +
        '<div class="inspector-field"><label>Kind</label><input value="' + escapeHTML(edge.data('kind') || 'basic') + '" readonly></div>' +
        '<div class="inspector-field"><label>Action</label><input value="' + escapeHTML(edge.data('label') || '') + '" readonly></div>';
});

// Clicking the background resets the inspector.
cy.on('tap', function(evt) {
    if (evt.target === cy) {
        document.getElementById('inspector-content').innerHTML =
            '<p class="hint">Click a component or connection to inspect.</p>';
    }
});

// --- Core functions ---

function addComponent() {
    var id = prompt('Component ID:');
    if (!id) return;
    var ifaceName = prompt('Interface name (optional):', id);
    if (ifaceName === null) ifaceName = id;

    cy.add({
        group: 'nodes',
        data: { id: id, label: id, iface: ifaceName }
    });

    cy.layout({ name: 'grid' }).run();
}

function addConnection() {
    var nodeIds = cy.nodes().map(function(n) { return n.data('id'); });
    if (nodeIds.length < 2) {
        alert('Add at least two components first.');
        return;
    }

    var source = prompt('Source component ID:\n(Available: ' + nodeIds.join(', ') + ')');
    if (!source) return;
    if (!cy.getElementById(source).isNode()) {
        alert('Component "' + source + '" not found.');
        return;
    }

    var target = prompt('Target component ID:');
    if (!target) return;
    if (!cy.getElementById(target).isNode()) {
        alert('Component "' + target + '" not found.');
        return;
    }

    var kind = prompt('Connection kind (basic / pipe / agent):', 'basic');
    if (!kind) kind = 'basic';
    if (['basic', 'pipe', 'agent'].indexOf(kind) === -1) {
        alert('Invalid kind. Must be basic, pipe, or agent.');
        return;
    }

    var actionName = prompt('Action name:', 'process');
    if (actionName === null) actionName = 'process';

    cy.add({
        group: 'edges',
        data: {
            source: source,
            target: target,
            kind: kind,
            label: actionName,
            actionName: actionName
        }
    });
}

// --- Save / Load ---

function graphToSchema() {
    var name = prompt('Architecture name:', 'my-architecture');
    if (!name) name = 'my-architecture';

    var components = [];
    cy.nodes().forEach(function(n) {
        var ifaceName = n.data('iface') || n.data('id');
        components.push({
            id: n.data('id'),
            interface: {
                name: ifaceName,
                actions: [],
                services: []
            }
        });
    });

    // Collect unique action names per component from edges targeting it.
    var componentActions = {};
    cy.edges().forEach(function(e) {
        var targetId = e.data('target');
        if (!componentActions[targetId]) componentActions[targetId] = [];
        var actionName = e.data('actionName') || e.data('label') || 'process';
        // Avoid duplicates.
        var exists = componentActions[targetId].some(function(a) { return a.name === actionName; });
        if (!exists) {
            componentActions[targetId].push({
                name: actionName,
                kind: 'in',
                params: []
            });
        }
    });

    // Merge collected actions into components.
    components.forEach(function(comp) {
        if (componentActions[comp.id]) {
            comp.interface.actions = componentActions[comp.id];
        }
    });

    var connections = [];
    cy.edges().forEach(function(e) {
        connections.push({
            from: e.data('source'),
            to: e.data('target'),
            kind: e.data('kind') || 'basic',
            action_name: e.data('actionName') || e.data('label') || 'process'
        });
    });

    var layout = {};
    cy.nodes().forEach(function(n) {
        layout[n.data('id')] = { x: n.position('x'), y: n.position('y') };
    });

    return {
        name: name,
        components: components,
        connections: connections,
        layout: layout
    };
}

function saveArchitecture() {
    var schema = graphToSchema();
    if (!schema) return;

    fetch('/api/architectures', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(schema)
    })
    .then(function(resp) {
        if (!resp.ok) return resp.text().then(function(t) { throw new Error(t); });
        return resp.json();
    })
    .then(function(data) {
        currentArchID = data.id;
        alert('Saved! Architecture ID: ' + data.id);
    })
    .catch(function(err) {
        alert('Save failed: ' + err.message);
    });
}

function schemaToGraph(schema) {
    // Clear existing elements.
    cy.elements().remove();

    // Add nodes.
    (schema.components || []).forEach(function(comp) {
        var pos = (schema.layout && schema.layout[comp.id]) || null;
        var nodeData = {
            id: comp.id,
            label: comp.id,
            iface: comp.interface ? comp.interface.name : comp.id
        };
        var nodeSpec = { group: 'nodes', data: nodeData };
        if (pos) {
            nodeSpec.position = { x: pos.x, y: pos.y };
        }
        cy.add(nodeSpec);
    });

    // Add edges.
    (schema.connections || []).forEach(function(conn, i) {
        cy.add({
            group: 'edges',
            data: {
                id: 'e' + i,
                source: conn.from,
                target: conn.to,
                kind: conn.kind || 'basic',
                label: conn.action_name || '',
                actionName: conn.action_name || ''
            }
        });
    });

    // If no layout was provided, run the grid layout.
    if (!schema.layout || Object.keys(schema.layout).length === 0) {
        cy.layout({ name: 'grid' }).run();
    }

    cy.fit();
}

function loadArchitecture() {
    var id = prompt('Architecture ID to load:');
    if (!id) return;

    fetch('/api/architectures/' + encodeURIComponent(id))
    .then(function(resp) {
        if (!resp.ok) return resp.text().then(function(t) { throw new Error(t); });
        return resp.json();
    })
    .then(function(schema) {
        currentArchID = id;
        schemaToGraph(schema);
    })
    .catch(function(err) {
        alert('Load failed: ' + err.message);
    });
}

// --- Simulation control ---

function startSim() {
    var id = currentArchID;
    if (!id) {
        id = prompt('Architecture ID to simulate:');
        if (!id) return;
    }

    // Ensure WebSocket is connected before starting.
    if (!ws || ws.readyState !== WebSocket.OPEN) {
        connectWS();
    }

    // Reset event feed.
    eventCount = 0;
    document.getElementById('event-count').textContent = '(0)';
    document.getElementById('event-list').innerHTML = '';

    fetch('/api/simulate/start/' + encodeURIComponent(id), { method: 'POST' })
    .then(function(resp) {
        if (!resp.ok) return resp.text().then(function(t) { throw new Error(t); });
        return resp.json();
    })
    .then(function(data) {
        document.getElementById('sim-status').textContent = 'Simulation running';
        document.getElementById('btn-start').disabled = true;
        document.getElementById('btn-stop').disabled = false;
    })
    .catch(function(err) {
        alert('Start simulation failed: ' + err.message);
    });
}

function stopSim() {
    fetch('/api/simulate/stop', { method: 'POST' })
    .then(function(resp) {
        if (!resp.ok) return resp.text().then(function(t) { throw new Error(t); });
        return resp.json();
    })
    .then(function(data) {
        document.getElementById('sim-status').textContent = '';
        document.getElementById('btn-start').disabled = false;
        document.getElementById('btn-stop').disabled = true;
    })
    .catch(function(err) {
        alert('Stop simulation failed: ' + err.message);
    });
}

function injectEvent() {
    var name = prompt('Event name:');
    if (!name) return;

    var paramsStr = prompt('Event params (JSON object, or empty):', '{}');
    var params = {};
    if (paramsStr) {
        try {
            params = JSON.parse(paramsStr);
        } catch (e) {
            alert('Invalid JSON for params.');
            return;
        }
    }

    fetch('/api/simulate/inject', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: name, params: params })
    })
    .then(function(resp) {
        if (!resp.ok) return resp.text().then(function(t) { throw new Error(t); });
        return resp.json();
    })
    .then(function(data) {
        console.log('Event injected:', data);
    })
    .catch(function(err) {
        alert('Inject failed: ' + err.message);
    });
}

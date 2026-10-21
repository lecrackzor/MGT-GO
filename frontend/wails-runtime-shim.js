// Wails Runtime Shim
// This provides a shim for @wailsio/runtime until Wails injects the real runtime
// Wails v3 uses HTTP fetch to communicate with the backend

// Try to use real Wails runtime if available, otherwise use shim
let useRealRuntime = false;
let realCall = null;

// Function to check for Wails runtime (will be called multiple times)
function checkForWailsRuntime() {
    // Check multiple possible locations for Wails runtime
    // Wails v3 may inject it at different locations
    if (window._wails) {
        const wailsKeys = Object.keys(window._wails || {});
        if (window._wails.Call && typeof window._wails.Call.ByID === 'function') {
            useRealRuntime = true;
            realCall = window._wails.Call;
            return true;
        }
        if (window._wails.runtime && window._wails.runtime.Call && typeof window._wails.runtime.Call.ByID === 'function') {
            useRealRuntime = true;
            realCall = window._wails.runtime.Call;
            return true;
        }
        for (const key of wailsKeys) {
            const value = window._wails[key];
            if (value && typeof value === 'object' && value.Call && typeof value.Call.ByID === 'function') {
                useRealRuntime = true;
                realCall = value.Call;
                return true;
            }
        }
    }

    if (window.Wails && window.Wails.Call && typeof window.Wails.Call.ByID === 'function') {
        useRealRuntime = true;
        realCall = window.Wails.Call;
        return true;
    }
    
    return false;
}

checkForWailsRuntime();

// Get runtime URL - Wails v3 uses a specific endpoint
// Check runtime-debug.js for the actual endpoint Wails uses
let runtimeURL = null;

// Try to get runtime URL from Wails runtime if available
if (window._wails) {
    if (window._wails.runtimeURL) {
        runtimeURL = window._wails.runtimeURL;
    } else if (window._wails.runtime && window._wails.runtime.runtimeURL) {
        runtimeURL = window._wails.runtime.runtimeURL;
    }
}

if (!runtimeURL) {
    runtimeURL = window.location.origin + '/wails/runtime';
}
let clientId = '';

// Generate client ID
function generateClientId() {
    if (!clientId) {
        clientId = 'client-' + Math.random().toString(36).substr(2, 9) + '-' + Date.now();
    }
    return clientId;
}

// Runtime caller implementation
async function runtimeCallWithID(objectID, methodID, args, callId = null) {
    // ALWAYS check for real runtime first (it might have been injected since last check)
    checkForWailsRuntime();

    if (useRealRuntime && realCall && typeof realCall.ByID === 'function') {
        try {
            return await realCall.ByID(methodID, ...(args || []));
        } catch (error) {
            console.error('[Shim] Error calling real Wails runtime:', error);
            console.error('[Shim] Error details:', error.message, error.stack);
            // Don't fall through - if real runtime fails, that's a real error
            throw error;
        }
    }
    
    // Real runtime is not available. Match its request shape from here.
    
    // Use shim implementation - match the real Wails format exactly
    // Real Wails runtime does NOT include call-id in body, only {object, method, args}
    const url = new URL(runtimeURL);
    const body = {
        object: objectID,
        method: methodID
    };
    if (args && args.length > 0) {
        body.args = args;
    }
    
    const headers = {
        'x-wails-client-id': generateClientId(),
        'Content-Type': 'application/json'
    };
    
    
    try {
        const response = await fetch(url, {
            method: 'POST',
            headers: headers,
            body: JSON.stringify(body)
        });
        
        if (!response.ok) {
            const errorText = await response.text();
            console.error('[Shim] Runtime call failed:', response.status, errorText);
            throw new Error(`Runtime call failed: ${response.status} - ${errorText}`);
        }
        
        const contentType = response.headers.get('Content-Type');
        let result;
        if (contentType && contentType.indexOf('application/json') !== -1) {
            result = await response.json();
        } else {
            result = await response.text();
        }
        return result;
    } catch (error) {
        console.error('[Shim] Runtime call exception for method', methodID, ':', error);
        console.error('[Shim] Exception details:', error.message, error.stack);
        throw error;
    }
}

// Generate unique ID for call tracking (matches Wails nanoid format)
function generateCallID() {
    // Wails uses nanoid - simplified version for compatibility
    const alphabet = 'useandom-26T198340PX75pxJACKVERYMINDBUSHWOLF_GQZbfghjklqvwyzrict';
    let id = '';
    for (let i = 0; i < 21; i++) {
        id += alphabet[Math.floor(Math.random() * alphabet.length)];
    }
    return id;
}

// Call implementation - matches Wails v3 format
// For binding calls, Wails expects call-id in the request body
function Call(options) {
    // Generate call-id like the real Wails runtime does
    const callId = generateCallID();
    
    return new Promise((resolve, reject) => {
        // For binding calls (objectID 0 = CallBinding), include call-id in the request
        // The real Wails runtime passes call-id to the binding caller
        const objectID = options.object || 0;
        const methodID = options.methodID || options.methodName;
        const args = options.args || [];
        
        // Use a special binding call format that includes call-id
        const request = bindingCallWithID(objectID, methodID, args, callId);
        
        request.then(resolve, reject);
    });
}

// Binding call implementation - matches real Wails format exactly
// Real Wails: call7(CallBinding, Object.assign({ "call-id": id }, options))
// call7 = newRuntimeCaller(objectNames.Call) where objectNames.Call = 0
// So: runtimeCallWithID(0, CallBinding (0), "", { "call-id": id, methodID: ..., args: [...] })
async function bindingCallWithID(objectID, methodID, args, callId) {
    // Check for real runtime first
    if (!useRealRuntime) {
        checkForWailsRuntime();
    }
    
    if (useRealRuntime && realCall && typeof realCall.ByID === 'function') {
        try {
            const result = await realCall.ByID(methodID, ...(args || []));
            return result;
        } catch (error) {
            console.error('[Shim] Real runtime binding call failed:', error);
            throw error;
        }
    }
    
    // Use shim - match real Wails format exactly
    // Real Wails calls: runtimeCallWithID(0, 0, "", { "call-id": id, methodID: ..., args: [...] })
    // So the body should be: { object: 0, method: 0, args: { "call-id": id, methodID: ..., args: [...] } }
    const url = new URL(runtimeURL);
    
    // Real Wails format: the options object (with call-id) becomes the args parameter
    const body = {
        object: 0,  // objectNames.Call
        method: 0,  // CallBinding
        args: {
            "call-id": callId,
            methodID: methodID,
            args: args || []
        }
    };
    
    const headers = {
        'x-wails-client-id': generateClientId(),
        'Content-Type': 'application/json'
    };
    
    try {
        const response = await fetch(url, {
            method: 'POST',
            headers: headers,
            body: JSON.stringify(body)
        });
        
        if (!response.ok) {
            const errorText = await response.text();
            throw new Error(`Binding call failed: ${response.status} - ${errorText}`);
        }
        
        const contentType = response.headers.get('Content-Type');
        if (contentType && contentType.indexOf('application/json') !== -1) {
            return await response.json();
        } else {
            return await response.text();
        }
    } catch (error) {
        console.error('[Shim] Binding call exception:', error);
        throw error;
    }
}

Call.ByID = function(methodID, ...args) {
    return Call({ methodID, args });
};

Call.ByName = function(methodName, ...args) {
    return Call({ methodName, args });
};

// CancellablePromise (simplified - just use regular Promise)
class CancellablePromise extends Promise {
    static withResolvers() {
        let resolve, reject;
        const promise = new Promise((res, rej) => {
            resolve = res;
            reject = rej;
        });
        return { promise, resolve, reject };
    }
}

// Create function with helper methods for type creation
function Create(obj) {
    return obj;
}

// Add helper methods for type creation (used by generated bindings)
Create.Array = function(itemType) {
    return function(arr) {
        if (!Array.isArray(arr)) return arr;
        return arr.map(item => itemType ? itemType(item) : item);
    };
};

Create.Map = function(keyType, valueType) {
    return function(map) {
        if (!map || typeof map !== 'object') return map;
        const result = {};
        for (const [key, value] of Object.entries(map)) {
            const processedKey = keyType ? keyType(key) : key;
            const processedValue = valueType ? valueType(value) : value;
            result[processedKey] = processedValue;
        }
        return result;
    };
};

Create.Nullable = function(type) {
    return function(value) {
        if (value === null || value === undefined) return null;
        return type ? type(value) : value;
    };
};

Create.Any = function(value) {
    return value;
};

// Events API - simplified implementation
const eventListeners = new Map();

function On(eventName, callback) {
    if (!eventListeners.has(eventName)) {
        eventListeners.set(eventName, []);
    }
    eventListeners.get(eventName).push(callback);
    
    // Return unsubscribe function
    return () => {
        const listeners = eventListeners.get(eventName);
        if (listeners) {
            const index = listeners.indexOf(callback);
            if (index > -1) {
                listeners.splice(index, 1);
            }
        }
    };
}

function Emit(eventName, data) {
    // If real Wails runtime is available, use it
    if (window._wails && window._wails.Events && window._wails.Events.Emit) {
        return window._wails.Events.Emit(eventName, data);
    }
    
    // Otherwise, dispatch to local listeners
    const listeners = eventListeners.get(eventName);
    if (listeners) {
        listeners.forEach(callback => {
            try {
                callback(data);
            } catch (error) {
                console.error('Error in event listener:', error);
            }
        });
    }
}

// Listen for events from backend (via window message or Wails runtime)
if (typeof window !== 'undefined') {
    // Listen for Wails events dispatched by the runtime
    if (window._wails && window._wails.dispatchWailsEvent) {
        const originalDispatch = window._wails.dispatchWailsEvent;
        window._wails.dispatchWailsEvent = function(event) {
            // Call original dispatch
            if (originalDispatch) {
                originalDispatch(event);
            }
            // Also dispatch to our listeners
            const listeners = eventListeners.get(event.name);
            if (listeners) {
                listeners.forEach(callback => {
                    try {
                        callback(event.data);
                    } catch (error) {
                        console.error('Error in event listener:', error);
                    }
                });
            }
        };
    }
}

const Events = {
    On,
    Emit,
    Off: On, // Alias for On (returns unsubscribe function)
    Once: (eventName, callback) => {
        const unsubscribe = On(eventName, (data) => {
            unsubscribe();
            callback(data);
        });
        return unsubscribe;
    }
};

export { Call, CancellablePromise, Create, Events };

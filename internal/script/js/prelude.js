// The script API: Postman's pm object, and Bruno's bru, req and res over
// the same functions, so that imported scripts run as they are.
//
// host is made by script.go. It has the request as JSON, the response if
// there is one, and functions that read and change the variables.
(function (host) {
  'use strict';

  var R = JSON.parse(host.request);
  R.headers = R.headers || [];
  R.params = R.params || [];
  R.body = R.body || { kind: 'none' };
  host.exportRequest = function () { return JSON.stringify(R); };

  var response = host.response ? JSON.parse(host.response) : null;

  // str is how a value is kept in a variable or a header.
  function str(v) {
    if (typeof v === 'string') return v;
    if (v !== null && typeof v === 'object') {
      try { return JSON.stringify(v); } catch (e) { /* as String below */ }
    }
    return String(v);
  }

  // ---- console

  function show(v) {
    return v instanceof Error ? String(v.stack || v) : str(v);
  }
  var console = {};
  ['log', 'info', 'warn', 'error', 'debug'].forEach(function (level) {
    console[level] = function () {
      host.log(level, Array.prototype.map.call(arguments, show).join(' '));
    };
  });
  globalThis.console = console;

  // ---- libraries, loaded when first used

  function lazy(target, name, load) {
    Object.defineProperty(target, name, {
      configurable: true,
      get: function () {
        var value = load();
        Object.defineProperty(target, name, { value: value, writable: true, configurable: true });
        return value;
      },
      set: function (value) {
        Object.defineProperty(target, name, { value: value, writable: true, configurable: true });
      },
    });
  }
  lazy(globalThis, 'CryptoJS', function () { return require('crypto-js'); });
  lazy(globalThis, 'expect', function () { return require('chai').expect; });

  // ---- variables

  function scope(name) {
    return {
      get: function (key) { return host.getVar(name, String(key)); },
      set: function (key, value) { host.setVar(name, String(key), str(value)); },
      unset: function (key) { host.unsetVar(name, String(key)); },
      has: function (key) { return host.getVar(name, String(key)) !== undefined; },
      toObject: function () { return host.vars(name); },
    };
  }
  var environment = scope('environment');
  var collection = scope('collection');
  var globals = scope('global');
  var runtime = scope('runtime');
  // pm.variables reads through every scope, narrowest first, and writes
  // to the run's own.
  var variables = {
    get: function (key) { return host.getVar('', String(key)); },
    has: function (key) { return host.getVar('', String(key)) !== undefined; },
    set: runtime.set,
    unset: runtime.unset,
    replaceIn: function (text) { return host.replaceIn(String(text)); },
  };

  // ---- the request

  function findHeader(name) {
    name = String(name).toLowerCase();
    for (var i = R.headers.length - 1; i >= 0; i--) {
      var h = R.headers[i];
      if (h.enabled && h.key.toLowerCase() === name) return h;
    }
  }
  function addHeader(key, value) {
    R.headers.push({ key: String(key), value: str(value), enabled: true });
  }
  function setHeader(key, value) {
    var h = findHeader(key);
    if (h) h.value = str(value); else addHeader(key, value);
  }
  function removeHeader(name) {
    name = String(name).toLowerCase();
    R.headers = R.headers.filter(function (h) { return h.key.toLowerCase() !== name; });
  }
  function headersObject() {
    var o = {};
    R.headers.forEach(function (h) { if (h.enabled) o[h.key] = h.value; });
    return o;
  }
  function setRawBody(value) {
    R.body.raw = str(value);
    if (!R.body.kind || R.body.kind === 'none') R.body.kind = 'json';
  }

  // The parts of the URL as it will be sent: variables resolved, and the
  // parameters in the query.
  function sentURL() { return host.url(JSON.stringify(R)); }
  var url = {
    toString: function () { return R.url; },
    getHost: function () { return sentURL().host; },
    getPath: function () { return sentURL().path; },
    getQueryString: function () { return sentURL().query; },
  };

  var modes = { json: 'raw', urlencoded: 'urlencoded', 'form-data': 'formdata' };
  var request = {
    get method() { return R.method; },
    set method(v) { R.method = String(v); },
    get url() { return url; },
    set url(v) { R.url = String(v); },
    headers: {
      get: function (name) { var h = findHeader(name); return h ? h.value : undefined; },
      has: function (name) { return !!findHeader(name); },
      add: function (h) { addHeader(h.key, h.value); },
      upsert: function (h) { setHeader(h.key, h.value); },
      remove: removeHeader,
      toObject: headersObject,
    },
    body: {
      get mode() { return modes[R.body.kind]; },
      get raw() { return R.body.raw || ''; },
      set raw(v) { setRawBody(v); },
      toString: function () { return R.body.kind === 'json' ? R.body.raw || '' : ''; },
    },
  };

  // ---- the response

  function responseHeader(name) {
    name = String(name).toLowerCase();
    for (var i = 0; i < response.headers.length; i++) {
      if (response.headers[i][0].toLowerCase() === name) return response.headers[i][1];
    }
  }
  var parsed, isParsed = false;
  function responseJSON() {
    if (!isParsed) { parsed = JSON.parse(host.responseBody); isParsed = true; }
    return parsed;
  }
  function fail(message) { throw new Error(message); }

  var pmResponse;
  if (response) {
    pmResponse = {
      code: response.code,
      status: response.status,
      responseTime: response.responseTime,
      responseSize: response.size,
      headers: {
        get: responseHeader,
        has: function (name) { return responseHeader(name) !== undefined; },
        toObject: function () {
          var o = {};
          response.headers.forEach(function (h) { o[h[0]] = h[1]; });
          return o;
        },
      },
      text: function () { return host.responseBody; },
      json: responseJSON,
      to: {
        have: {
          status: function (want) {
            if (typeof want === 'number') {
              if (response.code !== want) fail('expected response to have status code ' + want + ' but got ' + response.code);
            } else if (response.status !== String(want)) {
              fail('expected response to have status reason \'' + want + '\' but got \'' + response.status + '\'');
            }
          },
          header: function (name, value) {
            var got = responseHeader(name);
            if (got === undefined) fail('expected response to have header with key \'' + name + '\'');
            if (value !== undefined && got !== String(value)) {
              fail('expected \'' + name + '\' response header to be \'' + value + '\' but got \'' + got + '\'');
            }
          },
        },
        be: {
          get ok() {
            if (response.code !== 200) fail('expected response to have status code 200 but got ' + response.code);
            return true;
          },
          get success() {
            if (response.code < 200 || response.code > 299) fail('expected response code to be 2XX but found ' + response.code);
            return true;
          },
        },
      },
    };
  }

  // ---- tests

  function test(name, fn) {
    try {
      fn();
      host.test(String(name), true, '');
    } catch (e) {
      host.test(String(name), false, e && e.message !== undefined ? String(e.message) : String(e));
    }
  }

  // ---- Postman

  var pm = {
    info: { requestName: R.name, requestId: R.id, eventName: response ? 'test' : 'prerequest' },
    environment: environment,
    collectionVariables: collection,
    globals: globals,
    variables: variables,
    request: request,
    response: pmResponse,
    test: test,
  };
  lazy(pm, 'expect', function () { return globalThis.expect; });
  globalThis.pm = pm;

  // ---- Bruno

  globalThis.bru = {
    getEnvVar: environment.get,
    setEnvVar: environment.set,
    hasEnvVar: environment.has,
    deleteEnvVar: environment.unset,
    getGlobalEnvVar: globals.get,
    setGlobalEnvVar: globals.set,
    getCollectionVar: collection.get,
    getVar: runtime.get,
    setVar: runtime.set,
    hasVar: runtime.has,
    deleteVar: runtime.unset,
    interpolate: variables.replaceIn,
  };

  globalThis.req = {
    getMethod: function () { return R.method; },
    setMethod: function (v) { R.method = String(v); },
    getUrl: function () { return R.url; },
    setUrl: function (v) { R.url = String(v); },
    getHeader: request.headers.get,
    getHeaders: headersObject,
    setHeader: setHeader,
    setHeaders: function (o) { Object.keys(o).forEach(function (k) { setHeader(k, o[k]); }); },
    removeHeader: removeHeader,
    // A JSON body is an object to Bruno's scripts, not its text.
    getBody: function () {
      if (R.body.kind !== 'json') return undefined;
      try { return JSON.parse(R.body.raw); } catch (e) { return R.body.raw; }
    },
    setBody: setRawBody,
  };

  if (response) {
    var res = {
      status: response.code,
      statusText: response.status,
      responseTime: response.responseTime,
      headers: {},
      getStatus: function () { return response.code; },
      getHeader: responseHeader,
      getHeaders: function () { return res.headers; },
      getBody: function () { return res.body; },
      getResponseTime: function () { return response.responseTime; },
    };
    response.headers.forEach(function (h) {
      var key = h[0].toLowerCase();
      res.headers[key] = key in res.headers ? res.headers[key] + ', ' + h[1] : h[1];
    });
    lazy(res, 'body', function () {
      try { return responseJSON(); } catch (e) { return host.responseBody; }
    });
    globalThis.res = res;
  }

  globalThis.test = test;
})

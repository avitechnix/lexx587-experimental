import Toybox.Application;
import Toybox.Communications;
import Toybox.Lang;
import Toybox.Time;
import Toybox.Timer;
import Toybox.WatchUi;

// Vehicle state plus the Tessie API calls that read and change it.
// Only one request runs at a time; the watch relays them through the phone.
class Car {

    private const API = "https://api.tessie.com";

    var name as String = "My Tesla";
    var battery as Number? = null;
    var rangeMiles as Float? = null;
    var locked as Boolean? = null;
    var climateOn as Boolean? = null;
    var frunkOpen as Boolean = false;
    var trunkOpen as Boolean = false;
    var insideTempC as Float? = null;
    var chargingState as String? = null;
    var asleep as Boolean = false;
    var metric as Boolean = false;
    var celsius as Boolean = false;
    var updatedAt as Number? = null;

    // One line shown at the bottom of the status screen.
    var message as String? = null;
    var busy as Boolean = false;
    // The Tessie command in flight, so its button can show progress.
    var pending as String? = null;

    private var _doneMessage as String = "";
    private var _timer as Timer.Timer = new Timer.Timer();

    function initialize() {
        loadSummary();
    }

    function refresh() as Void {
        if (busy) {
            return;
        }
        var token = getToken();
        if (token == null) {
            message = "Add your Tessie token in the Garmin Connect app";
            WatchUi.requestUpdate();
            return;
        }
        busy = true;
        message = "Updating...";
        WatchUi.requestUpdate();

        var vin = getVin();
        if (vin == null) {
            Communications.makeWebRequest(API + "/vehicles", {"only_active" => true},
                requestOptions(token, Communications.HTTP_REQUEST_METHOD_GET), method(:onVehicles));
        } else {
            Communications.makeWebRequest(API + "/" + vin + "/state", null,
                requestOptions(token, Communications.HTTP_REQUEST_METHOD_GET), method(:onState));
        }
    }

    // command is a Tessie command name, e.g. "lock" or "start_climate".
    function sendCommand(command as String) as Void {
        var token = getToken();
        var vin = getVin();
        if (token == null || vin == null) {
            refresh();
            return;
        }
        if (busy) {
            message = "Busy, try again";
            WatchUi.requestUpdate();
            return;
        }
        var messages = commandMessages(command);
        busy = true;
        pending = command;
        _doneMessage = messages[1];
        message = messages[0] + "...";
        WatchUi.requestUpdate();

        // Tessie wakes the car if needed and answers once the command has run.
        Communications.makeWebRequest(API + "/" + vin + "/command/" + command + "?wait_for_completion=true",
            {}, requestOptions(token, Communications.HTTP_REQUEST_METHOD_POST), method(:onCommand));
    }

    function onVehicles(code as Number, data as Dictionary or String or Null) as Void {
        busy = false;
        if (code != 200 || !(data instanceof Dictionary)) {
            fail(code);
            return;
        }
        var results = data["results"];
        if (!(results instanceof Array) || results.size() == 0) {
            message = "No cars found in your Tessie account";
            WatchUi.requestUpdate();
            return;
        }
        var first = results[0] as Dictionary;
        Application.Storage.setValue("vin", first["vin"] as String);
        var state = first["last_state"];
        if (state instanceof Dictionary) {
            applyState(state);
        }
        message = null;
        WatchUi.requestUpdate();
    }

    function onState(code as Number, data as Dictionary or String or Null) as Void {
        busy = false;
        if (code != 200 || !(data instanceof Dictionary)) {
            fail(code);
            return;
        }
        applyState(data);
        message = null;
        WatchUi.requestUpdate();
    }

    function onCommand(code as Number, data as Dictionary or String or Null) as Void {
        var command = pending as String;
        busy = false;
        pending = null;
        var ok = code == 200 && data instanceof Dictionary && data["result"] == true;
        if (!ok) {
            if (code == 200) {
                message = "The car didn't respond";
                WatchUi.requestUpdate();
            } else {
                fail(code);
            }
            return;
        }

        // Show the result right away; Tessie's cached state can lag a few seconds.
        if (command.equals("lock")) {
            locked = true;
        } else if (command.equals("unlock")) {
            locked = false;
        } else if (command.equals("start_climate")) {
            climateOn = true;
        } else if (command.equals("stop_climate")) {
            climateOn = false;
        } else if (command.equals("activate_front_trunk")) {
            frunkOpen = true;
        } else if (command.equals("activate_rear_trunk")) {
            trunkOpen = !trunkOpen;
        }
        asleep = false;
        saveSummary();
        message = _doneMessage;
        WatchUi.requestUpdate();
        _timer.start(method(:refresh), 5000, false);
    }

    private function applyState(state as Dictionary) as Void {
        var displayName = state["display_name"];
        if (displayName instanceof String && displayName.length() > 0) {
            name = displayName;
        }
        asleep = "asleep".equals(state["state"] as String?);

        var charge = state["charge_state"];
        if (charge instanceof Dictionary) {
            battery = toNumber(charge["battery_level"] as Object?);
            rangeMiles = toFloat(charge["battery_range"] as Object?);
            var charging = charge["charging_state"];
            chargingState = charging instanceof String ? charging : null;
        }
        var climate = state["climate_state"];
        if (climate instanceof Dictionary) {
            var isClimateOn = climate["is_climate_on"];
            climateOn = isClimateOn instanceof Boolean ? isClimateOn : null;
            insideTempC = toFloat(climate["inside_temp"] as Object?);
        }
        var vehicle = state["vehicle_state"];
        if (vehicle instanceof Dictionary) {
            var isLocked = vehicle["locked"];
            locked = isLocked instanceof Boolean ? isLocked : null;
            // ft and rt are 0 when closed.
            var ft = toNumber(vehicle["ft"] as Object?);
            var rt = toNumber(vehicle["rt"] as Object?);
            frunkOpen = ft != null && ft > 0;
            trunkOpen = rt != null && rt > 0;
        }
        var gui = state["gui_settings"];
        if (gui instanceof Dictionary) {
            metric = "km/hr".equals(gui["gui_distance_units"] as String?);
            celsius = "C".equals(gui["gui_temperature_units"] as String?);
        }
        updatedAt = Time.now().value();
        saveSummary();
    }

    private function fail(code as Number) as Void {
        if (code == 401 || code == 403) {
            message = "Tessie rejected the token";
        } else if (code == 404) {
            message = "Car not found, check the VIN";
        } else if (code == Communications.BLE_CONNECTION_UNAVAILABLE) {
            message = "Phone not connected";
        } else if (code == Communications.NETWORK_REQUEST_TIMED_OUT || code == Communications.BLE_HOST_TIMEOUT || code == 408) {
            message = "Timed out, the car may be asleep";
        } else if (code == Communications.NETWORK_RESPONSE_TOO_LARGE || code == Communications.NETWORK_RESPONSE_OUT_OF_MEMORY) {
            message = "Response too large for the watch";
        } else {
            message = "Error " + code;
        }
        WatchUi.requestUpdate();
    }

    // [in progress, done] wording for the status line.
    private function commandMessages(command as String) as [String, String] {
        if (command.equals("lock")) {
            return ["Locking", "Locked"];
        } else if (command.equals("unlock")) {
            return ["Unlocking", "Unlocked"];
        } else if (command.equals("start_climate")) {
            return ["Starting climate", "Climate on"];
        } else if (command.equals("stop_climate")) {
            return ["Stopping climate", "Climate off"];
        } else if (command.equals("activate_front_trunk")) {
            return ["Opening frunk", "Frunk open"];
        } else if (command.equals("activate_rear_trunk")) {
            return trunkOpen ? ["Closing trunk", "Trunk closed"] : ["Opening trunk", "Trunk open"];
        } else if (command.equals("honk")) {
            return ["Honking", "Honked"];
        } else if (command.equals("flash")) {
            return ["Flashing lights", "Lights flashed"];
        }
        return ["Sending", "Done"];
    }

    function rangeText() as String? {
        if (rangeMiles == null) {
            return null;
        }
        if (metric) {
            return (rangeMiles * 1.609344).format("%d") + " km";
        }
        return rangeMiles.format("%d") + " mi";
    }

    function insideTempText() as String? {
        if (insideTempC == null) {
            return null;
        }
        if (celsius) {
            return insideTempC.format("%d") + "°C";
        }
        return (insideTempC * 9 / 5 + 32).format("%d") + "°F";
    }

    // Read by the glance, which cannot make network calls of its own.
    private function saveSummary() as Void {
        Application.Storage.setValue("summary", {
            "name" => name,
            "battery" => battery,
            "range" => rangeText(),
            "locked" => locked,
            "charging" => "Charging".equals(chargingState)
        });
    }

    private function loadSummary() as Void {
        var summary = Application.Storage.getValue("summary");
        if (summary instanceof Dictionary) {
            var savedName = summary["name"];
            if (savedName instanceof String) {
                name = savedName;
            }
            battery = summary["battery"] as Number?;
            locked = summary["locked"] as Boolean?;
        }
    }

    private function requestOptions(token as String, method as Communications.HttpRequestMethod) as {
            :method as Communications.HttpRequestMethod,
            :headers as Dictionary,
            :responseType as Communications.HttpResponseContentType
        } {
        return {
            :method => method,
            :headers => {
                "Authorization" => "Bearer " + token,
                "Content-Type" => Communications.REQUEST_CONTENT_TYPE_JSON
            },
            :responseType => Communications.HTTP_RESPONSE_CONTENT_TYPE_JSON
        };
    }

    private function getToken() as String? {
        return nonEmpty(Application.Properties.getValue("tessieToken"));
    }

    private function getVin() as String? {
        var vin = nonEmpty(Application.Properties.getValue("vin"));
        return vin != null ? vin : nonEmpty(Application.Storage.getValue("vin"));
    }

    private function nonEmpty(value as Object?) as String? {
        if (value instanceof String) {
            var trimmed = trim(value);
            return trimmed.length() > 0 ? trimmed : null;
        }
        return null;
    }

    // Pasted tokens often carry stray spaces or newlines.
    private function trim(s as String) as String {
        var chars = s.toCharArray();
        var start = 0;
        var end = chars.size();
        while (start < end && chars[start].toNumber() <= 32) {
            start++;
        }
        while (end > start && chars[end - 1].toNumber() <= 32) {
            end--;
        }
        return s.substring(start, end) as String;
    }

    private function toNumber(value as Object?) as Number? {
        return (value instanceof Number || value instanceof Float || value instanceof Double || value instanceof Long)
            ? value.toNumber() : null;
    }

    private function toFloat(value as Object?) as Float? {
        return (value instanceof Number || value instanceof Float || value instanceof Double || value instanceof Long)
            ? value.toFloat() : null;
    }
}

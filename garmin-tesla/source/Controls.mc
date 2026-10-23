import Toybox.Lang;
import Toybox.WatchUi;

class StatusDelegate extends WatchUi.BehaviorDelegate {

    private var _car as Car;

    function initialize(car as Car) {
        BehaviorDelegate.initialize();
        _car = car;
    }

    function onSelect() as Boolean {
        showControls(_car);
        return true;
    }

    function onMenu() as Boolean {
        showControls(_car);
        return true;
    }
}

// Each item id is the Tessie command name it sends.
function showControls(car as Car) as Void {
    var menu = new WatchUi.Menu2({:title => "Controls"});
    var locked = car.locked;
    if (locked == true) {
        menu.addItem(new WatchUi.MenuItem("Unlock", "Currently locked", "unlock", null));
        menu.addItem(new WatchUi.MenuItem("Lock", null, "lock", null));
    } else {
        menu.addItem(new WatchUi.MenuItem("Lock", locked == false ? "Currently unlocked" : null, "lock", null));
        menu.addItem(new WatchUi.MenuItem("Unlock", null, "unlock", null));
    }
    if (car.climateOn == true) {
        menu.addItem(new WatchUi.MenuItem("Climate off", "Currently on", "stop_climate", null));
        menu.addItem(new WatchUi.MenuItem("Climate on", null, "start_climate", null));
    } else {
        menu.addItem(new WatchUi.MenuItem("Climate on", null, "start_climate", null));
        menu.addItem(new WatchUi.MenuItem("Climate off", null, "stop_climate", null));
    }
    menu.addItem(new WatchUi.MenuItem("Open frunk", null, "activate_front_trunk", null));
    menu.addItem(new WatchUi.MenuItem("Trunk", "Opens or closes", "activate_rear_trunk", null));
    menu.addItem(new WatchUi.MenuItem("Honk", null, "honk", null));
    menu.addItem(new WatchUi.MenuItem("Flash lights", null, "flash", null));
    menu.addItem(new WatchUi.MenuItem("Refresh", null, "refresh", null));
    WatchUi.pushView(menu, new ControlsDelegate(car), WatchUi.SLIDE_UP);
}

class ControlsDelegate extends WatchUi.Menu2InputDelegate {

    private var _car as Car;

    function initialize(car as Car) {
        Menu2InputDelegate.initialize();
        _car = car;
    }

    function onSelect(item as WatchUi.MenuItem) as Void {
        var command = item.getId() as String;
        var label = item.getLabel();
        // Back to the status screen first, so it shows the command's progress.
        WatchUi.popView(WatchUi.SLIDE_DOWN);

        if (command.equals("refresh")) {
            _car.refresh();
        } else if (command.equals("unlock") || command.equals("activate_front_trunk") || command.equals("activate_rear_trunk")) {
            WatchUi.pushView(new WatchUi.Confirmation(label + "?"),
                new ConfirmDelegate(_car, command, label), WatchUi.SLIDE_IMMEDIATE);
        } else {
            _car.sendCommand(command, label);
        }
    }
}

// Asks before anything that opens the car.
class ConfirmDelegate extends WatchUi.ConfirmationDelegate {

    private var _car as Car;
    private var _command as String;
    private var _label as String;

    function initialize(car as Car, command as String, label as String) {
        ConfirmationDelegate.initialize();
        _car = car;
        _command = command;
        _label = label;
    }

    function onResponse(response as WatchUi.Confirm) as Boolean {
        if (response == WatchUi.CONFIRM_YES) {
            _car.sendCommand(_command, _label);
        }
        return true;
    }
}

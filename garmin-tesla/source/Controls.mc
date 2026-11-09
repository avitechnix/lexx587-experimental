import Toybox.Attention;
import Toybox.Lang;
import Toybox.WatchUi;

// Quick actions on the home screen. Locking and climate run on a tap. Anything
// that opens the car (unlock, frunk, trunk) needs a press and hold instead, so
// a brushed screen can't open it.
class StatusDelegate extends WatchUi.BehaviorDelegate {

    private var _car as Car;
    private var _view as StatusView;

    function initialize(car as Car, view as StatusView) {
        BehaviorDelegate.initialize();
        _car = car;
        _view = view;
    }

    function onTap(event as WatchUi.ClickEvent) as Boolean {
        var xy = event.getCoordinates();
        var button = _view.buttonAt(xy[0], xy[1]);
        if (button == StatusView.BUTTON_LOCK) {
            if (_car.locked == true) {
                hint("Hold to unlock");
            } else {
                run("lock");
            }
        } else if (button == StatusView.BUTTON_CLIMATE) {
            run(_car.climateOn == true ? "stop_climate" : "start_climate");
        } else if (button == StatusView.BUTTON_FRUNK) {
            hint(_car.frunkOpen ? "Close the frunk by hand" : "Hold to open frunk");
        } else if (button == StatusView.BUTTON_TRUNK) {
            hint(_car.trunkOpen ? "Hold to close trunk" : "Hold to open trunk");
        } else {
            showMore(_car);
        }
        return true;
    }

    function onHold(event as WatchUi.ClickEvent) as Boolean {
        var xy = event.getCoordinates();
        var button = _view.buttonAt(xy[0], xy[1]);
        if (button == StatusView.BUTTON_LOCK) {
            run(_car.locked == true ? "unlock" : "lock");
        } else if (button == StatusView.BUTTON_CLIMATE) {
            run(_car.climateOn == true ? "stop_climate" : "start_climate");
        } else if (button == StatusView.BUTTON_FRUNK) {
            if (_car.frunkOpen) {
                hint("Close the frunk by hand");
            } else {
                run("activate_front_trunk");
            }
        } else if (button == StatusView.BUTTON_TRUNK) {
            run("activate_rear_trunk");
        } else {
            return false;
        }
        return true;
    }

    // A screen tap also arrives as Select and a hold as Menu, and those come
    // first. Declining them lets onTap and onHold see where the touch was;
    // the physical buttons then reach onKey.
    function onSelect() as Boolean {
        return false;
    }

    function onMenu() as Boolean {
        return false;
    }

    function onKey(event as WatchUi.KeyEvent) as Boolean {
        var key = event.getKey();
        if (key == WatchUi.KEY_ENTER || key == WatchUi.KEY_MENU) {
            showMore(_car);
            return true;
        }
        return false;
    }

    private function run(command as String) as Void {
        buzz(40);
        _car.sendCommand(command);
    }

    private function hint(text as String) as Void {
        buzz(15);
        _car.message = text;
        WatchUi.requestUpdate();
    }

    private function buzz(ms as Number) as Void {
        if (Attention has :vibrate) {
            Attention.vibrate([new Attention.VibeProfile(60, ms)]);
        }
    }
}

// The less frequent actions.
function showMore(car as Car) as Void {
    var menu = new WatchUi.Menu2({:title => "More"});
    menu.addItem(new WatchUi.MenuItem("Honk", null, "honk", null));
    menu.addItem(new WatchUi.MenuItem("Flash lights", null, "flash", null));
    menu.addItem(new WatchUi.MenuItem("Refresh", "Get the latest status", "refresh", null));
    WatchUi.pushView(menu, new MoreDelegate(car), WatchUi.SLIDE_UP);
}

class MoreDelegate extends WatchUi.Menu2InputDelegate {

    private var _car as Car;

    function initialize(car as Car) {
        Menu2InputDelegate.initialize();
        _car = car;
    }

    function onSelect(item as WatchUi.MenuItem) as Void {
        var command = item.getId() as String;
        // Back to the home screen first, so it shows the command's progress.
        WatchUi.popView(WatchUi.SLIDE_DOWN);
        if (command.equals("refresh")) {
            _car.refresh();
        } else {
            _car.sendCommand(command);
        }
    }
}

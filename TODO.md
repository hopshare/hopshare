# TODO

## Running local Postgres
    podman run --detach \
    --name postgres \
    -e POSTGRES_USER=hopuser \
    -e POSTGRES_PASSWORD=hoppass \
    -e POSTGRES_DB=hopshare \
    -e POSTGRES_ADMIN_PASSWORD=adminpass \
    -v postgres_data:/var/lib/postgresql/data:Z \
    -p 5432:5432 \
    docker.io/library/postgres:17.7


## Bugs

* Login should take username. Usernames should be unique at the service level- do not take email address.
* You can offer to help the same Hop multiple times
* Accepting Help on a Hop that has been Canceled should not be an error- just a message that the Hop was canceled already.
* An Organization Owner can request membership in their own Organization- this should be prevented
* Don't show the "Remove" button on the row for the primary Organization Owner when they go to the Manage Organization page
* Hop Filters say "Created" instead of "Pending" in Your Hops page
* Double check all the messaging on the MyHopShare dashboard- not sure they are correct as Hours numbers change
* Race condition when multiple users sign up at the same time with the same First and Last name. The first one in will win as username must be unique. There is some code in here to detect unique constraint violation but it's not working.


## Now

* My HopShare Dashboard
    * "Change..." for Organization should be a pulldown of all Organizations Member belongs to. 
    * At bottom of that card, have a link "Find an Organization..." that goes to Find Organization page.
* My Profile
    * Have a link to let you manage your Organization or create a new one.
* Manage Organization page
    * Add location, description, etc.
    * Stack Membership Requests horizontally below Org Details
    * Remove "Back To My Hopshare" button- make a link like Messages/My Hops
* Find Organization Page
    * Long Organization names do not fit into the search results- should put those into larger results- with ability to drill into details on the organization before asking to join.
* Joining an Organization should use messages
    * Send an Action message to all Organization Owners when asking to join an Organization. This should handle the organization membership action automatically. Send all Owners another message with the results of an action taken by any Owner. It should not be possible to Reject after it's been Accepted by another Owner.
    * Send yourself an information message that you requested membership in an Organization.
* Organization "Wall"- closest thing to 'social media' feature- inspire others.
* Owners are moderators for listings- they can flag/delete inappropriate requests/comments
* Organizations need to have a readable URL for new joiners. A way for users and non-users to sign up quickly.
* Manage Skills on the Member profile page. We will need something for automatic matching...give it some thought. Skills should reside in the database- we can seed some starter ones, but it should grow over time- and be scoped within the organization. We can have these configured for new joiners via a wizard interface.
* Administrator page- see everything, do dangerous stuff. Link conditionally off header menu for Admin users.

## Later

* Add ability to add comments to a completed Request.
* Create a 'celebration' page for the Organization?
* Make service/ExpireHelpRequests() asynchronous- we should start a goroutine that runs daily to clear these out (not only when the myhpopshare page is rendered).




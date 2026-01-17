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

* You can offer to help the same Hop multiple times
* Accepting Help on a Hop that has been Canceled should not be an error- just a message that the Hop was canceled already.
* An Organization Owner can request membership in their own Organization- this should be prevented
* Hop Filters say "Created" instead of "Pending" in Your Hops page
* Double check all the messaging on the MyHopShare dashboard- not sure they are correct as Hours numbers change


## Now

* Add location to Organization- that can be searched by.
* Organization "Wall"- closest thing to 'social media' feature- inspire others.
* Owners are moderators for listings- they can flag/delete inappropriate requests/comments
* Organizations need to have a readable URL for new joiners. A way for users and non-users to sign up quickly.
* Manage Skills on the Member profile page. We will need something for automatic matching...give it some thought. Skills should reside in the database- we can seed some starter ones, but it should grow over time- and be scoped within the organization. We can have these configured for new joiners via a wizard interface.
* Administrator page- see everything, do dangerous stuff. Link conditionally off header menu for Admin users.

## Later

* Add ability to add comments to a completed Request.
* Create a 'celebration' page for the Organization?
* Make service/ExpireHelpRequests() asynchronous- we should start a goroutine that runs daily to clear these out (not only when the myhpopshare page is rendered).



